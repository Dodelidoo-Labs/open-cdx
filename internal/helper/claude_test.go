package helper

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	collector := newClaudeCollector()
	now := time.Unix(2_000_000_000, 0).UTC()
	identity := claudecode.AccountIdentity("account")
	window := []claudecode.Window{{Seconds: 18000, UsedPercent: 5, ResetAt: now.Add(time.Hour).Format(time.RFC3339)}}
	collector.addReading(claudeReading{session: "s1", observedAt: now, receivedAt: now, windows: window})
	collector.addReading(claudeReading{session: "unknown", observedAt: now, receivedAt: now, windows: window})

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
	report, _ = collector.take(now.Add(40 * time.Second))
	if len(report.Allowances) != 1 || report.Allowances[0].Account != "" || len(report.Accounts) != 0 {
		t.Fatalf("unattributed reading after waiting = %#v", report)
	}

	collector.addLogs(claudecode.LogBatch{Requests: []claudecode.Request{{ID: "retry"}}}, now)
	failed, readings := collector.take(now)
	collector.addReading(claudeReading{session: "s1", observedAt: now.Add(time.Minute), receivedAt: now, windows: window})
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
	daemon := &Daemon{localSecret: testLocalSecret, remote: &RemoteClient{BaseURL: router.URL, DeviceToken: "device-token", HTTP: router.Client()}, claude: newClaudeCollector(), shutdown: make(chan struct{})}
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
	daemon := &Daemon{remote: &RemoteClient{BaseURL: router.URL, DeviceToken: "device-token", HTTP: router.Client()}, claude: newClaudeCollector()}
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
	if len(accounts) != 1 || accounts[0].ObservedAt == nil || accounts[0].Plan != "Claude" || len(accounts[0].QuotaWindows) != 2 ||
		accounts[0].QuotaWindows[1].ResetAt != nil || accounts[0].QuotaWindows[0].Remaining != 36 {
		t.Fatalf("Claude accounts = %#v", accounts)
	}
}
