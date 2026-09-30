package helper

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/claudecode"
)

const testLocalSecret = "012345678901234567890123456789012345"

func TestScopedTokenAuthenticatesOnlyItsScope(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	token, err := IssueScopedToken(testLocalSecret, ClaudeOTLPScope, now)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyScopedToken(testLocalSecret, ClaudeOTLPScope, token, now.Add(59*time.Minute)) {
		t.Fatal("fresh scoped token was rejected")
	}
	if VerifyScopedToken(testLocalSecret, ClaudeOTLPScope, token, now.Add(62*time.Minute)) ||
		VerifyScopedToken(testLocalSecret, "other", token, now) ||
		VerifyScopedToken("different-secret-012345678901234567890", ClaudeOTLPScope, token, now) {
		t.Fatal("expired, rescoped, or foreign token was accepted")
	}
	if VerifyLocalToken(testLocalSecret, token, now) {
		t.Fatal("a telemetry token authenticated inference")
	}
	local, _ := IssueLocalToken(testLocalSecret, now)
	if VerifyScopedToken(testLocalSecret, ClaudeOTLPScope, local, now) {
		t.Fatal("an inference token authenticated telemetry")
	}
	if _, err = IssueScopedToken(testLocalSecret, "a.b", now); err == nil {
		t.Fatal("scope with a separator was accepted")
	}
}

func TestClaudeCollectorAttributesStatusLineThroughSessions(t *testing.T) {
	collector := newClaudeCollector("")
	now := time.Unix(2_000_000_000, 0).UTC()
	identity := claudecode.AccountIdentity("account")
	window := []claudecode.Window{{Seconds: 18000, UsedPercent: 5, ResetAt: now.Add(time.Hour).Format(time.RFC3339)}}
	collector.addReading(claudeReading{session: "s1", observedAt: now, receivedAt: now, windows: window}, nil)
	collector.addReading(claudeReading{session: "unknown", observedAt: now, receivedAt: now, windows: window}, nil)

	report, taken := collector.take(now.Add(5 * time.Second))
	if len(report.Allowances) != 0 || len(taken) != 0 {
		t.Fatalf("reading without an account was sent immediately: %#v", report)
	}
	collector.addLogs(claudecode.LogBatch{
		Sessions: map[string]string{"s1": identity}, Accounts: []claudecode.Account{{Identity: identity, MaskedEmail: "a***@e***.com"}},
		Requests: []claudecode.Request{{ID: "req", Model: "claude-opus-5-5", Account: identity}},
	}, now)
	report, taken = collector.take(now.Add(10 * time.Second))
	if len(report.Requests) != 1 || len(report.Allowances) != 1 || report.Allowances[0].Account != identity || len(taken) != 1 ||
		len(report.Accounts) != 1 || report.Accounts[0].MaskedEmail != "a***@e***.com" {
		t.Fatalf("attributed report = %#v", report)
	}
	// Without its session's account, the reading belongs to the only known one.
	report, _ = collector.take(now.Add(40 * time.Second))
	if len(report.Allowances) != 1 || report.Allowances[0].Account != identity || len(report.Accounts) != 1 {
		t.Fatalf("reading after waiting = %#v", report)
	}

	collector.addLogs(claudecode.LogBatch{Requests: []claudecode.Request{{ID: "retry"}}}, now)
	failed, readings := collector.take(now)
	collector.addReading(claudeReading{session: "s1", observedAt: now.Add(time.Minute), receivedAt: now, windows: window}, nil)
	collector.restore(failed, append(readings, claudeReading{session: "s1", observedAt: now}))
	report, _ = collector.take(now.Add(time.Minute))
	if len(report.Requests) != 1 || report.Requests[0].ID != "retry" || len(report.Allowances) != 1 || !strings.HasPrefix(report.Allowances[0].ObservedAt, now.Add(time.Minute).Format("2006-01-02T15:04")) {
		t.Fatalf("restored report = %#v", report)
	}
}

func TestDaemonReceivesClaudeTelemetryAndUploadsIt(t *testing.T) {
	var mu sync.Mutex
	var uploads []claudecode.Report
	router := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/claude/telemetry" || request.Header.Get("Authorization") != "Bearer device-token" {
			http.NotFound(writer, request)
			return
		}
		var report claudecode.Report
		_ = json.NewDecoder(request.Body).Decode(&report)
		mu.Lock()
		uploads = append(uploads, report)
		mu.Unlock()
		_, _ = writer.Write([]byte(`{"requests_added":1}`))
	}))
	defer router.Close()
	daemon := &Daemon{localSecret: testLocalSecret, remote: &RemoteClient{BaseURL: router.URL, DeviceToken: "device-token", HTTP: router.Client()}, claude: newClaudeCollector(""), shutdown: make(chan struct{})}
	otlp := daemon.scopedAuth(ClaudeOTLPScope, http.HandlerFunc(daemon.claudeOTLPLogs))
	statusLine := daemon.localAuth(http.HandlerFunc(daemon.claudeStatusLine))

	payload := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"attributes":[
		{"key":"event.name","value":{"stringValue":"api_request"}},{"key":"session.id","value":{"stringValue":"s1"}},
		{"key":"user.account_uuid","value":{"stringValue":"uuid"}},{"key":"model","value":{"stringValue":"claude-opus-5-5"}},
		{"key":"request_id","value":{"stringValue":"req_1"}},{"key":"event.timestamp","value":{"stringValue":"2026-09-29T21:01:05.248Z"}},
		{"key":"input_tokens","value":{"intValue":3}}]}]}]}]}`
	send := func(handler http.Handler, path, token, contentType string, body io.Reader, encoding string) int {
		request := httptest.NewRequest(http.MethodPost, path, body)
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", contentType)
		if encoding != "" {
			request.Header.Set("Content-Encoding", encoding)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}
	now := time.Now().UTC()
	localToken, _ := IssueLocalToken(testLocalSecret, now)
	scopedToken, _ := IssueScopedToken(testLocalSecret, ClaudeOTLPScope, now)
	if code := send(otlp, claudecode.OTLPLogsPath, localToken, "application/json", strings.NewReader(payload), ""); code != http.StatusUnauthorized {
		t.Fatalf("inference token accepted by telemetry: %d", code)
	}
	if code := send(otlp, claudecode.OTLPLogsPath, scopedToken, "application/x-protobuf", strings.NewReader(payload), ""); code != http.StatusUnsupportedMediaType {
		t.Fatalf("protobuf status = %d", code)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write([]byte(payload))
	_ = writer.Close()
	if code := send(otlp, claudecode.OTLPLogsPath, scopedToken, "application/json", &compressed, "gzip"); code != http.StatusOK {
		t.Fatalf("telemetry status = %d", code)
	}
	reading, _ := json.Marshal(StatusLineUpload{SessionID: "s1", ObservedAt: now, Windows: []claudecode.Window{{Seconds: 18000, UsedPercent: 5, ResetAt: now.Add(time.Hour).Format(time.RFC3339)}}})
	if code := send(statusLine, "/claude/statusline", scopedToken, "application/json", bytes.NewReader(reading), ""); code != http.StatusUnauthorized {
		t.Fatalf("telemetry token accepted by status line: %d", code)
	}
	if code := send(statusLine, "/claude/statusline", localToken, "application/json", bytes.NewReader(reading), ""); code != http.StatusNoContent {
		t.Fatalf("status line status = %d", code)
	}
	if err := daemon.uploadClaudeTelemetry(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(uploads) != 1 || len(uploads[0].Requests) != 1 || uploads[0].Requests[0].ID != "req_1" || len(uploads[0].Allowances) != 1 ||
		uploads[0].Allowances[0].Account != claudecode.AccountIdentity("uuid") {
		t.Fatalf("uploads = %#v", uploads)
	}
	status := daemon.currentStatus().ClaudeCode
	if status.LastTelemetryAt == nil || status.LastStatusLineAt == nil || status.LastUploadAt == nil || status.PendingRequests != 0 {
		t.Fatalf("Claude Code status = %#v", status)
	}
}

func TestDaemonKeepsClaudeTelemetryWhileRouterIsUnavailable(t *testing.T) {
	status := http.StatusServiceUnavailable
	router := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(`{"error":{"message":"unavailable"}}`))
	}))
	defer router.Close()
	daemon := &Daemon{remote: &RemoteClient{BaseURL: router.URL, DeviceToken: "device-token", HTTP: router.Client()}, claude: newClaudeCollector("")}
	daemon.claude.addLogs(claudecode.LogBatch{Requests: []claudecode.Request{{ID: "req"}}}, time.Now())
	if err := daemon.uploadClaudeTelemetry(context.Background()); err == nil || daemon.claude.pending() != 1 || daemon.currentStatus().ClaudeCode.LastError == "" {
		t.Fatalf("router outage dropped telemetry: %v pending=%d", err, daemon.claude.pending())
	}
	status = http.StatusBadRequest
	if err := daemon.uploadClaudeTelemetry(context.Background()); err == nil || daemon.claude.pending() != 0 {
		t.Fatalf("rejected batch was retried forever: %v pending=%d", err, daemon.claude.pending())
	}
}

func TestDaemonStatusIncludesClaudeAccounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"route":{"connected":true},"claude_accounts":[{"id":"c1","masked_email":"a***@e***.com","observed_at":"2030-01-02T03:04:05Z",
			"device_name":"Mac","quota_windows":[{"label":"Weekly","remaining":36,"duration_minutes":10080,"reset_at":"2030-01-05T00:00:00Z"},
			{"label":"5 hours","remaining":100,"duration_minutes":300,"pace_marker_percent":0}]}]}`))
	}))
	defer server.Close()
	daemon := &Daemon{remote: &RemoteClient{BaseURL: server.URL, DeviceToken: "t", HTTP: server.Client()}}
	if err := daemon.refreshStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	accounts := daemon.currentStatus().ClaudeAccounts
	if len(accounts) != 1 || accounts[0].ObservedAt == nil || accounts[0].Provider != "claude" || len(accounts[0].QuotaWindows) != 2 ||
		accounts[0].QuotaWindows[1].ResetAt != nil || accounts[0].QuotaWindows[0].Remaining != 36 {
		t.Fatalf("Claude accounts = %#v", accounts)
	}
}

func TestClaudeCollectorRemembersSoleAccountAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-accounts.json")
	now := time.Unix(2_000_000_000, 0).UTC()
	identity := claudecode.AccountIdentity("account")
	first := newClaudeCollector(path)
	first.addLogs(claudecode.LogBatch{Accounts: []claudecode.Account{{Identity: identity, MaskedEmail: "a***@e***.com"}}}, now)

	restarted := newClaudeCollector(path)
	window := []claudecode.Window{{Seconds: 18000, UsedPercent: 5, ResetAt: now.Add(time.Hour).Format(time.RFC3339)}}
	restarted.addReading(claudeReading{session: "old-session", observedAt: now, receivedAt: now, windows: window}, nil)
	report, _ := restarted.take(now.Add(time.Minute))
	if len(report.Allowances) != 1 || report.Allowances[0].Account != identity || len(report.Accounts) != 1 || report.Accounts[0].MaskedEmail != "a***@e***.com" {
		t.Fatalf("reading was not attributed to the known account: %#v", report)
	}
	restarted.addLogs(claudecode.LogBatch{Accounts: []claudecode.Account{{Identity: claudecode.AccountIdentity("second")}}}, now)
	restarted.addReading(claudeReading{session: "old-session", observedAt: now, receivedAt: now, windows: window}, nil)
	if report, _ = restarted.take(now.Add(time.Minute)); report.Allowances[0].Account != "" {
		t.Fatalf("reading was guessed between two accounts: %#v", report)
	}
}

func TestStatusLineAccountAttributesReadingImmediately(t *testing.T) {
	collector := newClaudeCollector("")
	now := time.Unix(2_000_000_000, 0).UTC()
	account := claudecode.Account{Identity: claudecode.AccountIdentity("uuid"), MaskedEmail: "a***@e***.com"}
	window := []claudecode.Window{{Seconds: 18000, UsedPercent: 5, ResetAt: now.Add(time.Hour).Format(time.RFC3339)}}
	collector.addReading(claudeReading{session: "no-telemetry", observedAt: now, receivedAt: now, windows: window}, &account)
	report, _ := collector.take(now)
	if len(report.Allowances) != 1 || report.Allowances[0].Account != account.Identity || len(report.Accounts) != 1 || report.Accounts[0].MaskedEmail != account.MaskedEmail {
		t.Fatalf("report = %#v", report)
	}
}

func TestClaudeUsageCheckQueuesReadingOnlyWhenConnected(t *testing.T) {
	directory := t.TempDir()
	claudeHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	if err := os.WriteFile(filepath.Join(claudeHome, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"uuid","emailAddress":"someone@example.com"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	reset := now.Add(3 * 24 * time.Hour).In(time.UTC)
	output, _ := json.Marshal(map[string]any{"is_error": false, "result": "Current week (all models): 69% used · resets " +
		reset.Format("Jan 2 at 3:04pm") + " (UTC)\n"})
	calls := 0
	daemon := &Daemon{configPath: filepath.Join(directory, "helper.json"), claude: newClaudeCollector(""), shutdown: make(chan struct{}),
		claudeUsage: func(context.Context) ([]byte, error) { calls++; return output, nil }}

	if err := daemon.checkClaudeUsage(context.Background(), true); err != nil || calls != 0 {
		t.Fatalf("checked while Claude Code is not connected: calls=%d err=%v", calls, err)
	}
	if err := os.WriteFile(filepath.Join(directory, ClaudeStateFile), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	daemon.updateStatus(func(status *LocalStatus) { status.ClaudeCode.LastStatusLineAt = timePointer(now) })
	if err := daemon.checkClaudeUsage(context.Background(), false); err != nil || calls != 0 {
		t.Fatalf("timer checked while the status line is fresh: calls=%d err=%v", calls, err)
	}
	if err := daemon.checkClaudeUsage(context.Background(), true); err != nil || calls != 1 {
		t.Fatalf("forced check: calls=%d err=%v", calls, err)
	}
	report, _ := daemon.claude.take(time.Now().UTC())
	if len(report.Allowances) != 1 || report.Allowances[0].Account != claudecode.AccountIdentity("uuid") ||
		len(report.Allowances[0].Windows) != 1 || report.Allowances[0].Windows[0].UsedPercent != 69 {
		t.Fatalf("report = %#v", report)
	}
	if status := daemon.currentStatus().ClaudeCode; status.LastUsageCheckAt == nil || status.UsageCheckError != "" {
		t.Fatalf("status = %#v", status)
	}

	daemon.claudeUsage = func(context.Context) ([]byte, error) { return nil, errors.New("claude /usage failed: exit status 1") }
	if err := daemon.checkClaudeUsage(context.Background(), true); err == nil {
		t.Fatal("a failed check reported success")
	}
	if status := daemon.currentStatus().ClaudeCode; status.UsageCheckError == "" || status.LastStatusLineAt == nil {
		t.Fatalf("status = %#v", status)
	}
	if report, _ = daemon.claude.take(time.Now().UTC()); len(report.Allowances) != 0 {
		t.Fatalf("a failed check queued a reading: %#v", report)
	}
}
