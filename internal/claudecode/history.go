package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxTranscriptLineBytes = 64 << 20

// History is the usage found in local Claude Code transcripts. Claude Code
// writes one transcript entry per content block, so entries are merged by
// Anthropic request ID. Auxiliary requests that Claude Code does not keep in
// transcripts, such as session titles, are only visible through live telemetry.
type History struct {
	Home             string    `json:"-"`
	FilesScanned     int       `json:"files_scanned"`
	Requests         []Request `json:"-"`
	Models           int       `json:"models"`
	InputTokens      int64     `json:"input_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	MalformedRecords int       `json:"malformed_records_skipped"`
	OversizeRecords  int       `json:"oversize_records_skipped"`
	FirstAt          string    `json:"first_at,omitempty"`
	LastAt           string    `json:"last_at,omitempty"`
}

type HistoryPreview struct {
	Home string `json:"home"`
	History
	RequestCount int `json:"requests"`
}

func (history History) Preview() HistoryPreview {
	return HistoryPreview{Home: history.Home, History: history, RequestCount: len(history.Requests)}
}

// DefaultHome follows Claude Code's CLAUDE_CONFIG_DIR override.
func DefaultHome() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", errors.New("CLAUDE_CONFIG_DIR must be an absolute path")
		}
		return filepath.Clean(configured), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

type transcriptEntry struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			InputTokens         int64 `json:"input_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadTokens     int64 `json:"cache_read_input_tokens"`
			OutputTokens        int64 `json:"output_tokens"`
			OutputDetails       struct {
				ThinkingTokens int64 `json:"thinking_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	} `json:"message"`
}

// ScanHistory reads every transcript under home/projects, including
// subagent transcripts. Only assistant usage records are decoded.
func ScanHistory(ctx context.Context, home string) (History, error) {
	history := History{Home: home}
	root := filepath.Join(home, "projects")
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return history, errors.New("no Claude Code transcripts were found in " + root)
	}
	requests := make(map[string]*Request)
	firstSeen := make(map[string]time.Time)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		history.FilesScanned++
		return scanTranscript(path, &history, requests, firstSeen)
	})
	if err != nil {
		return History{}, err
	}
	models := make(map[string]struct{})
	history.Requests = make([]Request, 0, len(requests))
	var first, last time.Time
	for key, request := range requests {
		at := firstSeen[key]
		request.At = at.UTC().Format(time.RFC3339Nano)
		history.Requests = append(history.Requests, *request)
		models[request.Model] = struct{}{}
		history.InputTokens += request.InputTokens + request.CacheReadTokens + request.CacheCreationTokens
		history.OutputTokens += request.OutputTokens
		if first.IsZero() || at.Before(first) {
			first = at
		}
		if at.After(last) {
			last = at
		}
	}
	sort.Slice(history.Requests, func(i, j int) bool {
		if history.Requests[i].At != history.Requests[j].At {
			return history.Requests[i].At < history.Requests[j].At
		}
		return history.Requests[i].ID < history.Requests[j].ID
	})
	history.Models = len(models)
	if !first.IsZero() {
		history.FirstAt, history.LastAt = first.UTC().Format(time.RFC3339), last.UTC().Format(time.RFC3339)
	}
	return history, nil
}

var (
	assistantMarker = []byte(`"assistant"`)
	usageMarker     = []byte(`"usage"`)
)

func scanTranscript(path string, history *History, requests map[string]*Request, firstSeen map[string]time.Time) error {
	file, err := os.Open(path)
	if err != nil {
		// A transcript can disappear while Claude Code cleans up old sessions.
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 256<<10)
	for {
		line, oversize, err := readTranscriptLine(reader)
		if oversize {
			history.OversizeRecords++
		} else if len(line) > 0 && bytes.Contains(line, assistantMarker) && bytes.Contains(line, usageMarker) {
			recordTranscriptLine(line, history, requests, firstSeen)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func recordTranscriptLine(line []byte, history *History, requests map[string]*Request, firstSeen map[string]time.Time) {
	var entry transcriptEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		history.MalformedRecords++
		return
	}
	usage := entry.Message.Usage
	model := strings.TrimSpace(entry.Message.Model)
	if entry.Type != "assistant" || usage == nil || model == "" || strings.HasPrefix(model, "<") {
		return
	}
	id := entry.RequestID
	if id == "" && entry.Message.ID != "" {
		id = "message:" + entry.Message.ID
	}
	at, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if id == "" || err != nil {
		history.MalformedRecords++
		return
	}
	request := requests[id]
	if request == nil {
		request = &Request{ID: id, Model: model}
		requests[id] = request
		firstSeen[id] = at
	} else if at.Before(firstSeen[id]) {
		firstSeen[id] = at
	}
	// Content blocks of one response repeat its usage; streaming snapshots
	// may be partial, so keep the largest value seen for each counter.
	request.InputTokens = max(request.InputTokens, usage.InputTokens)
	request.CacheReadTokens = max(request.CacheReadTokens, usage.CacheReadTokens)
	request.CacheCreationTokens = max(request.CacheCreationTokens, usage.CacheCreationTokens)
	request.OutputTokens = max(request.OutputTokens, usage.OutputTokens)
	request.ThinkingTokens = max(request.ThinkingTokens, usage.OutputDetails.ThinkingTokens)
}

func readTranscriptLine(reader *bufio.Reader) ([]byte, bool, error) {
	var line []byte
	oversize := false
	for {
		chunk, err := reader.ReadSlice('\n')
		if !oversize {
			if len(line)+len(chunk) > maxTranscriptLineBytes {
				oversize, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimSpace(line), oversize, err
	}
}
