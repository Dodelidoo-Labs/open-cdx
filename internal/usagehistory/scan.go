package usagehistory

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	rolloutReadBufferBytes = 128 << 10
	maxRelevantRecordBytes = 8 << 20
)

type recordRelevance uint8

const (
	relevanceUnknown recordRelevance = iota
	relevanceNeeded
	relevanceIgnored
)

type tokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

type rowKey struct {
	day, provider, model string
}

type scanState struct {
	provider        string
	model           string
	turnID          string
	previous        *tokenUsage
	replaying       bool
	lastReplayUsage time.Time
}

func DefaultCodexHome() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CODEX_HOME")); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", errors.New("CODEX_HOME must be an absolute path")
		}
		return filepath.Clean(configured), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("Codex home directory is unavailable")
	}
	return filepath.Join(home, ".codex"), nil
}

// Scan reads only Codex rollout JSONL files under sessions and
// archived_sessions. Unknown records (including all prompt and response
// records) are discarded without decoding their payloads.
func Scan(ctx context.Context, codexHome string, now time.Time) (Snapshot, error) {
	snapshot := Snapshot{Version: SnapshotVersion, GeneratedAt: now.UTC().Format(time.RFC3339), Rows: make([]Row, 0)}
	paths, err := rolloutPaths(codexHome)
	if err != nil {
		return Snapshot{}, err
	}
	aggregates := make(map[rowKey]*Row)
	seen := make(map[[32]byte]struct{})
	for _, path := range paths {
		if err = ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		file, openErr := os.Open(filepath.Clean(path))
		if openErr != nil {
			return Snapshot{}, errors.New("a Codex rollout could not be opened")
		}
		snapshot.FilesScanned++
		scanErr := scanRollout(ctx, file, &snapshot, aggregates, seen)
		closeErr := file.Close()
		if scanErr != nil {
			return Snapshot{}, scanErr
		}
		if closeErr != nil {
			return Snapshot{}, errors.New("a Codex rollout could not be closed")
		}
	}
	for _, row := range aggregates {
		snapshot.Rows = append(snapshot.Rows, *row)
	}
	sort.Slice(snapshot.Rows, func(left, right int) bool {
		if snapshot.Rows[left].Day != snapshot.Rows[right].Day {
			return snapshot.Rows[left].Day < snapshot.Rows[right].Day
		}
		if snapshot.Rows[left].Provider != snapshot.Rows[right].Provider {
			return snapshot.Rows[left].Provider < snapshot.Rows[right].Provider
		}
		return snapshot.Rows[left].Model < snapshot.Rows[right].Model
	})
	return snapshot, nil
}

func rolloutPaths(codexHome string) ([]string, error) {
	if strings.TrimSpace(codexHome) == "" || !filepath.IsAbs(codexHome) {
		return nil, errors.New("Codex home must be an absolute path")
	}
	var paths []string
	for _, directory := range []string{"sessions", "archived_sessions"} {
		root := filepath.Join(codexHome, directory)
		info, err := os.Stat(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errors.New("Codex session history is not readable")
		}
		if !info.IsDir() {
			continue
		}
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, errors.New("Codex session history is not readable")
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func scanRollout(ctx context.Context, file *os.File, snapshot *Snapshot, aggregates map[rowKey]*Row, seen map[[32]byte]struct{}) error {
	state := scanState{}
	reader := bufio.NewReaderSize(file, rolloutReadBufferBytes)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, needed, malformed, err := nextRelevantRecord(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("a Codex rollout could not be read")
		}
		if malformed {
			snapshot.MalformedLines++
		}
		if !needed {
			continue
		}
		var header struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &header) != nil {
			snapshot.MalformedLines++
			continue
		}
		switch header.Type {
		case "session_meta":
			var record struct {
				Timestamp string `json:"timestamp"`
				Payload   struct {
					ModelProvider  string `json:"model_provider"`
					ParentThreadID string `json:"parent_thread_id"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &record) == nil {
				state.provider = strings.TrimSpace(record.Payload.ModelProvider)
				if strings.TrimSpace(record.Payload.ParentThreadID) != "" {
					state.replaying = true
					state.lastReplayUsage, _ = time.Parse(time.RFC3339Nano, record.Timestamp)
				}
			}
		case "turn_context":
			var record struct {
				Payload struct {
					TurnID string `json:"turn_id"`
					Model  string `json:"model"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &record) == nil {
				state.turnID = strings.TrimSpace(record.Payload.TurnID)
				if model := strings.TrimSpace(record.Payload.Model); model != "" {
					state.model = model
				}
			}
		case "event_msg":
			if err := scanEventLine(line, &state, snapshot, aggregates, seen); err != nil {
				snapshot.MalformedLines++
			}
		}
	}
	return nil
}

// nextRelevantRecord reads one logical JSONL record. Conversation records are
// identified from their small JSON prefix and stream-discarded, so even a very
// large prompt, image, or response is never retained in memory. Only the four
// small record families needed for accounting are assembled for JSON decoding.
func nextRelevantRecord(reader *bufio.Reader) ([]byte, bool, bool, error) {
	var record []byte
	relevance := relevanceUnknown
	malformed, sawBytes := false, false
	for {
		fragment, readErr := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			sawBytes = true
		}
		if relevance != relevanceIgnored {
			if len(record)+len(fragment) > maxRelevantRecordBytes {
				record = nil
				malformed = relevance == relevanceNeeded
				relevance = relevanceIgnored
			} else {
				record = append(record, fragment...)
				if relevance == relevanceUnknown {
					relevance = classifyRecordPrefix(record)
					if relevance == relevanceIgnored {
						record = nil
					}
				}
			}
		}
		if readErr == bufio.ErrBufferFull {
			continue
		}
		if readErr != nil && readErr != io.EOF {
			return nil, false, malformed, readErr
		}
		if !sawBytes && readErr == io.EOF {
			return nil, false, false, io.EOF
		}
		if relevance == relevanceUnknown {
			malformed = true
		}
		return bytes.TrimSuffix(record, []byte{'\n'}), relevance == relevanceNeeded, malformed, nil
	}
}

func classifyRecordPrefix(prefix []byte) recordRelevance {
	topLevel, nextOffset, found := nextTypeValue(prefix, 0)
	if !found {
		return relevanceUnknown
	}
	switch topLevel {
	case "session_meta", "turn_context":
		return relevanceNeeded
	case "event_msg":
		eventType, _, eventFound := nextTypeValue(prefix, nextOffset)
		if !eventFound {
			return relevanceUnknown
		}
		switch eventType {
		case "token_count", "turn_started", "thread_settings_applied", "model_reroute":
			return relevanceNeeded
		default:
			return relevanceIgnored
		}
	default:
		return relevanceIgnored
	}
}

func nextTypeValue(data []byte, offset int) (string, int, bool) {
	key := []byte(`"type"`)
	index := bytes.Index(data[offset:], key)
	if index < 0 {
		return "", offset, false
	}
	index += offset + len(key)
	for index < len(data) && (data[index] == ' ' || data[index] == '\t' || data[index] == '\r' || data[index] == '\n') {
		index++
	}
	if index >= len(data) || data[index] != ':' {
		return "", index, false
	}
	index++
	for index < len(data) && (data[index] == ' ' || data[index] == '\t') {
		index++
	}
	if index >= len(data) || data[index] != '"' {
		return "", index, false
	}
	index++
	end := bytes.IndexByte(data[index:], '"')
	if end < 0 {
		return "", index, false
	}
	end += index
	return string(data[index:end]), end + 1, true
}

func scanEventLine(line []byte, state *scanState, snapshot *Snapshot, aggregates map[rowKey]*Row, seen map[[32]byte]struct{}) error {
	var kind struct {
		Payload struct {
			Type string `json:"type"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &kind); err != nil {
		return err
	}
	switch kind.Payload.Type {
	case "turn_started":
		var record struct {
			Payload struct {
				TurnID string `json:"turn_id"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			return err
		}
		state.turnID = strings.TrimSpace(record.Payload.TurnID)
	case "thread_settings_applied":
		var record struct {
			Payload struct {
				ThreadSettings struct {
					Model           string `json:"model"`
					ModelProviderID string `json:"model_provider_id"`
				} `json:"thread_settings"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			return err
		}
		if model := strings.TrimSpace(record.Payload.ThreadSettings.Model); model != "" {
			state.model = model
		}
		if provider := strings.TrimSpace(record.Payload.ThreadSettings.ModelProviderID); provider != "" {
			state.provider = provider
		}
	case "model_reroute":
		var record struct {
			Payload struct {
				ToModel string `json:"to_model"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			return err
		}
		if model := strings.TrimSpace(record.Payload.ToModel); model != "" {
			state.model = model
		}
	case "token_count":
		return scanTokenCount(line, state, snapshot, aggregates, seen)
	}
	return nil
}

func scanTokenCount(line []byte, state *scanState, snapshot *Snapshot, aggregates map[rowKey]*Row, seen map[[32]byte]struct{}) error {
	var record struct {
		Timestamp string `json:"timestamp"`
		Ordinal   *int64 `json:"ordinal"`
		Payload   struct {
			Info *struct {
				Total *tokenUsage `json:"total_token_usage"`
				Last  *tokenUsage `json:"last_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &record); err != nil {
		return err
	}
	if record.Payload.Info == nil || (record.Payload.Info.Total == nil && record.Payload.Info.Last == nil) {
		return nil
	}
	when, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return err
	}
	provider, model := routeIdentity(state.provider, state.model)
	if model == "" {
		model = "unknown"
	}
	current := record.Payload.Info.Total
	skipReplay := false
	if state.replaying {
		if !state.lastReplayUsage.IsZero() && when.Sub(state.lastReplayUsage) > 2*time.Second {
			state.replaying = false
		} else {
			skipReplay = true
		}
		state.lastReplayUsage = when
	}
	var increment tokenUsage
	if current != nil {
		// last_token_usage is the usage for exactly one model response. The
		// cumulative counters are only a stable identity/baseline: they can be
		// copied into forks and rebased when a session is resumed, so subtracting
		// adjacent totals can turn a counter decrease into billions of phantom
		// tokens.
		if record.Payload.Info.Last != nil {
			increment = *record.Payload.Info.Last
		} else if state.previous == nil {
			increment = *current
		} else {
			increment = usageDelta(*current, *state.previous)
		}
		copy := *current
		state.previous = &copy
	} else {
		increment = *record.Payload.Info.Last
	}
	if skipReplay {
		return nil
	}
	if !increment.hasMeasuredUsage() {
		return nil
	}
	// Copied rollout history rewrites record timestamps but preserves the
	// cumulative usage snapshot. Hash that snapshot without the timestamp so
	// the same response is counted once across roots, resumes, forks, archives,
	// and subagent rollouts. Fall back to the timestamp only for old records
	// that do not carry cumulative counters.
	fingerprintInput := struct {
		Timestamp string
		Usage     tokenUsage
	}{Usage: dereferenceUsage(current, record.Payload.Info.Last)}
	if current == nil {
		fingerprintInput.Timestamp = record.Timestamp
	}
	encoded, _ := json.Marshal(fingerprintInput)
	fingerprint := sha256.Sum256(encoded)
	if _, duplicate := seen[fingerprint]; duplicate {
		snapshot.DuplicateEvents++
		return nil
	}
	seen[fingerprint] = struct{}{}
	key := rowKey{day: when.UTC().Format("2006-01-02"), provider: provider, model: model}
	row := aggregates[key]
	if row == nil {
		row = &Row{Day: key.day, Provider: key.provider, Model: key.model}
		aggregates[key] = row
	}
	row.Requests++
	row.InputTokens += increment.InputTokens
	row.CachedInputTokens += increment.CachedInputTokens
	row.CacheWriteInputTokens += increment.CacheWriteInputTokens
	row.OutputTokens += increment.OutputTokens
	row.ReasoningOutputTokens += increment.ReasoningOutputTokens
	snapshot.EventsImported++
	return nil
}

func usageDelta(current, previous tokenUsage) tokenUsage {
	return tokenUsage{
		InputTokens:           positiveDelta(current.InputTokens, previous.InputTokens),
		CachedInputTokens:     positiveDelta(current.CachedInputTokens, previous.CachedInputTokens),
		CacheWriteInputTokens: positiveDelta(current.CacheWriteInputTokens, previous.CacheWriteInputTokens),
		OutputTokens:          positiveDelta(current.OutputTokens, previous.OutputTokens),
		ReasoningOutputTokens: positiveDelta(current.ReasoningOutputTokens, previous.ReasoningOutputTokens),
		TotalTokens:           positiveDelta(current.TotalTokens, previous.TotalTokens),
	}
}

func positiveDelta(current, previous int64) int64 {
	if current < 0 {
		return 0
	}
	if current >= previous {
		return current - max(previous, 0)
	}
	// A counter reset starts a new cumulative sequence inside the same file.
	return current
}

func (usage tokenUsage) hasMeasuredUsage() bool {
	return usage.InputTokens > 0 || usage.CachedInputTokens > 0 || usage.CacheWriteInputTokens > 0 || usage.OutputTokens > 0 || usage.ReasoningOutputTokens > 0
}

func dereferenceUsage(primary, fallback *tokenUsage) tokenUsage {
	if primary != nil {
		return *primary
	}
	if fallback != nil {
		return *fallback
	}
	return tokenUsage{}
}

func routeIdentity(configuredProvider, model string) (string, string) {
	provider := strings.ToLower(strings.TrimSpace(configuredProvider))
	model = strings.TrimSpace(model)
	switch {
	case strings.HasPrefix(model, "openrouter/"):
		provider = "openrouter"
	case strings.HasPrefix(model, "ollama/"):
		provider = "ollama"
	case provider == "" || provider == "router" || provider == "opencdx" || provider == "chatgpt":
		provider = "openai"
	}
	if provider == "" {
		provider = "unknown"
	}
	return provider, model
}

func (snapshot Snapshot) Summary() string {
	return fmt.Sprintf("%d unique usage events from %d rollout files across %d daily model rows", snapshot.EventsImported, snapshot.FilesScanned, len(snapshot.Rows))
}
