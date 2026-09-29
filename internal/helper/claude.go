package helper

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/claudecode"
)

const (
	ClaudeOTLPScope          = "claude-otlp"
	claudeUploadInterval     = 5 * time.Second
	claudeSessionAttribution = 30 * time.Second
	claudeSessionRetention   = 48 * time.Hour
	maxPendingClaudeRequests = 20_000
	maxClaudeOTLPBodyBytes   = 16 << 20
)

// ClaudeCodeStatus tells the menu whether Claude Code is reporting.
type ClaudeCodeStatus struct {
	LastTelemetryAt  *time.Time `json:"last_telemetry_at,omitempty"`
	LastStatusLineAt *time.Time `json:"last_status_line_at,omitempty"`
	LastUploadAt     *time.Time `json:"last_upload_at,omitempty"`
	PendingRequests  int        `json:"pending_requests"`
	LastError        string     `json:"last_error,omitempty"`
}

// StatusLineUpload is what `router-helper claude-statusline` sends to the
// daemon: the session and its plan windows, nothing else from the status line.
type StatusLineUpload struct {
	SessionID  string              `json:"session_id"`
	ObservedAt time.Time           `json:"observed_at"`
	Windows    []claudecode.Window `json:"windows"`
}

type claudeReading struct {
	session    string
	observedAt time.Time
	receivedAt time.Time
	windows    []claudecode.Window
}

type claudeSession struct {
	identity string
	seen     time.Time
}

// claudeCollector buffers Claude Code observations between uploads. Nothing is
// written to disk; after a helper restart, transcript import fills any gap.
type claudeCollector struct {
	mu       sync.Mutex
	sessions map[string]claudeSession
	accounts map[string]string
	requests []claudecode.Request
	readings []claudeReading
}

func newClaudeCollector() *claudeCollector {
	return &claudeCollector{sessions: make(map[string]claudeSession), accounts: make(map[string]string)}
}

func (collector *claudeCollector) addLogs(batch claudecode.LogBatch, now time.Time) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	for session, identity := range batch.Sessions {
		collector.sessions[session] = claudeSession{identity: identity, seen: now}
	}
	for _, account := range batch.Accounts {
		if collector.accounts[account.Identity] == "" {
			collector.accounts[account.Identity] = account.MaskedEmail
		}
	}
	collector.requests = append(collector.requests, batch.Requests...)
	if overflow := len(collector.requests) - maxPendingClaudeRequests; overflow > 0 {
		collector.requests = append([]claudecode.Request(nil), collector.requests[overflow:]...)
	}
}

func (collector *claudeCollector) addReading(reading claudeReading) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	for index := range collector.readings {
		if collector.readings[index].session == reading.session {
			collector.readings[index] = reading
			return
		}
	}
	collector.readings = append(collector.readings, reading)
}

func (collector *claudeCollector) pending() int {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return len(collector.requests)
}

// take removes everything ready for upload. A status line reading waits briefly
// for its session's account to arrive through telemetry; without one it is
// attributed to this machine.
func (collector *claudeCollector) take(now time.Time) (claudecode.Report, []claudeReading) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	for session, state := range collector.sessions {
		if now.Sub(state.seen) > claudeSessionRetention {
			delete(collector.sessions, session)
		}
	}
	report := claudecode.Report{Version: claudecode.ReportVersion, Source: claudecode.SourceLive, Requests: collector.requests}
	collector.requests = nil
	referenced := make(map[string]struct{})
	for _, request := range report.Requests {
		if request.Account != "" {
			referenced[request.Account] = struct{}{}
		}
	}
	var taken, waiting []claudeReading
	for _, reading := range collector.readings {
		identity := collector.sessions[reading.session].identity
		if identity == "" && now.Sub(reading.receivedAt) < claudeSessionAttribution {
			waiting = append(waiting, reading)
			continue
		}
		taken = append(taken, reading)
		report.Allowances = append(report.Allowances, claudecode.Allowance{Account: identity, ObservedAt: reading.observedAt.UTC().Format(time.RFC3339Nano), Windows: reading.windows})
		if identity != "" {
			referenced[identity] = struct{}{}
		}
	}
	collector.readings = waiting
	for identity := range referenced {
		report.Accounts = append(report.Accounts, claudecode.Account{Identity: identity, MaskedEmail: collector.accounts[identity]})
	}
	return report, taken
}

// restore returns an unsent report to the front of the queue.
func (collector *claudeCollector) restore(report claudecode.Report, readings []claudeReading) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.requests = append(report.Requests, collector.requests...)
	if overflow := len(collector.requests) - maxPendingClaudeRequests; overflow > 0 {
		collector.requests = collector.requests[overflow:]
	}
	for _, reading := range readings {
		newer := false
		for _, queued := range collector.readings {
			newer = newer || queued.session == reading.session
		}
		if !newer {
			collector.readings = append(collector.readings, reading)
		}
	}
}

func (daemon *Daemon) scopedAuth(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		parts := strings.Fields(request.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !VerifyScopedToken(daemon.localSecret, scope, parts[1], time.Now().UTC()) {
			writer.Header().Set("WWW-Authenticate", "Bearer")
			writeHelperJSON(writer, http.StatusUnauthorized, map[string]any{"error": map[string]string{"type": "invalid_local_token", "message": "local helper authentication failed"}})
			return
		}
		request.Header.Del("Authorization")
		next.ServeHTTP(writer, request)
	})
}

// claudeOTLPLogs receives Claude Code's OTLP/HTTP JSON log export. Only
// api_request events are kept; the body is never logged or forwarded.
func (daemon *Daemon) claudeOTLPLogs(writer http.ResponseWriter, request *http.Request) {
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		writeHelperJSON(writer, http.StatusUnsupportedMediaType, map[string]string{"error": "set OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/json"})
		return
	}
	var body io.Reader = request.Body
	if strings.EqualFold(request.Header.Get("Content-Encoding"), "gzip") {
		reader, err := gzip.NewReader(request.Body)
		if err != nil {
			writeHelperJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid gzip body"})
			return
		}
		defer reader.Close()
		body = reader
	}
	raw, err := io.ReadAll(io.LimitReader(body, maxClaudeOTLPBodyBytes+1))
	if err != nil || len(raw) > maxClaudeOTLPBodyBytes {
		writeHelperJSON(writer, http.StatusRequestEntityTooLarge, map[string]string{"error": "telemetry batch is too large"})
		return
	}
	now := time.Now().UTC()
	batch, err := claudecode.ParseOTLPLogs(raw, now)
	if err != nil {
		writeHelperJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	daemon.claude.addLogs(batch, now)
	daemon.updateStatus(func(status *LocalStatus) {
		status.ClaudeCode.LastTelemetryAt = timePointer(now)
		status.ClaudeCode.PendingRequests = daemon.claude.pending()
	})
	// An empty ExportLogsServiceResponse reports full success.
	writeHelperJSON(writer, http.StatusOK, map[string]any{})
}

func (daemon *Daemon) claudeStatusLine(writer http.ResponseWriter, request *http.Request) {
	var upload StatusLineUpload
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&upload); err != nil || len(upload.SessionID) > 256 || len(upload.Windows) == 0 || len(upload.Windows) > 8 {
		http.Error(writer, "invalid status line reading", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	if upload.ObservedAt.IsZero() || upload.ObservedAt.After(now.Add(time.Minute)) {
		upload.ObservedAt = now
	}
	daemon.claude.addReading(claudeReading{session: upload.SessionID, observedAt: upload.ObservedAt, receivedAt: now, windows: upload.Windows})
	daemon.updateStatus(func(status *LocalStatus) { status.ClaudeCode.LastStatusLineAt = timePointer(now) })
	writer.WriteHeader(http.StatusNoContent)
}

func (daemon *Daemon) claudeLoop(ctx context.Context) {
	ticker := time.NewTicker(claudeUploadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-daemon.shutdown:
			return
		case <-ticker.C:
			_ = daemon.uploadClaudeTelemetry(ctx)
		}
	}
}

func (daemon *Daemon) uploadClaudeTelemetry(ctx context.Context) error {
	report, readings := daemon.claude.take(time.Now().UTC())
	if len(report.Requests) == 0 && len(report.Allowances) == 0 {
		return nil
	}
	uploadContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var result claudecode.Result
	response, err := daemon.remote.JSON(uploadContext, http.MethodPost, "/api/v1/claude/telemetry", report, &result, true)
	now := time.Now().UTC()
	if err != nil {
		// Retry after outages and server errors. A batch the router rejects as
		// invalid would be rejected again, so it is dropped.
		if response == nil || response.StatusCode != http.StatusBadRequest {
			daemon.claude.restore(report, readings)
		}
		daemon.updateStatus(func(status *LocalStatus) {
			status.ClaudeCode.LastError = err.Error()
			status.ClaudeCode.PendingRequests = daemon.claude.pending()
		})
		return err
	}
	daemon.updateStatus(func(status *LocalStatus) {
		status.ClaudeCode.LastUploadAt = timePointer(now)
		status.ClaudeCode.LastError = ""
		status.ClaudeCode.PendingRequests = daemon.claude.pending()
	})
	if len(report.Allowances) > 0 {
		// Show a new allowance in the menu without waiting for the next poll.
		go func() {
			refreshContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = daemon.refreshStatus(refreshContext)
		}()
	}
	return nil
}
