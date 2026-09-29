package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// otlpFixture mirrors the shape Claude Code 2.1.285 exports with
// OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/json. Values are synthetic.
const otlpFixture = `{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},
"scopeLogs":[{"scope":{"name":"com.anthropic.claude_code.events"},"logRecords":[
{"timeUnixNano":"1790715664056000000","body":{"stringValue":"claude_code.user_prompt"},"attributes":[
 {"key":"session.id","value":{"stringValue":"session-1"}},{"key":"user.email","value":{"stringValue":"someone@example.com"}},
 {"key":"user.account_uuid","value":{"stringValue":"account-uuid-1"}},{"key":"event.name","value":{"stringValue":"user_prompt"}},
 {"key":"prompt","value":{"stringValue":"SECRET PROMPT TEXT"}}]},
{"timeUnixNano":"1790715665248000000","body":{"stringValue":"claude_code.api_request"},"attributes":[
 {"key":"session.id","value":{"stringValue":"session-1"}},{"key":"user.email","value":{"stringValue":"someone@example.com"}},
 {"key":"user.account_uuid","value":{"stringValue":"account-uuid-1"}},{"key":"event.name","value":{"stringValue":"api_request"}},
 {"key":"event.timestamp","value":{"stringValue":"2026-09-29T21:01:05.248Z"}},{"key":"model","value":{"stringValue":"claude-haiku-4-5-20251001"}},
 {"key":"input_tokens","value":{"intValue":10}},{"key":"output_tokens","value":{"intValue":"39"}},
 {"key":"cache_read_tokens","value":{"intValue":25192}},{"key":"cache_creation_tokens","value":{"intValue":11802}},
 {"key":"cost_usd","value":{"doubleValue":0.026}},{"key":"request_id","value":{"stringValue":"req_1"}}]},
{"timeUnixNano":"1790715666000000000","body":{"stringValue":"claude_code.assistant_response"},"attributes":[
 {"key":"event.name","value":{"stringValue":"assistant_response"}},{"key":"response","value":{"stringValue":"SECRET RESPONSE"}}]}
]}]}]}`

func TestParseOTLPLogsKeepsOnlyRequestCounters(t *testing.T) {
	batch, err := ParseOTLPLogs([]byte(otlpFixture), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Requests) != 1 {
		t.Fatalf("requests = %#v", batch.Requests)
	}
	request := batch.Requests[0]
	identity := AccountIdentity("account-uuid-1")
	if request.ID != "req_1" || request.Model != "claude-haiku-4-5-20251001" || request.Account != identity ||
		request.InputTokens != 10 || request.OutputTokens != 39 || request.CacheReadTokens != 25192 || request.CacheCreationTokens != 11802 ||
		request.At != "2026-09-29T21:01:05.248Z" {
		t.Fatalf("request = %#v", request)
	}
	if len(batch.Accounts) != 1 || batch.Accounts[0].Identity != identity || batch.Accounts[0].MaskedEmail != "s***e@e***.com" {
		t.Fatalf("accounts = %#v", batch.Accounts)
	}
	if batch.Sessions["session-1"] != identity {
		t.Fatalf("sessions = %#v", batch.Sessions)
	}
	encoded, _ := json.Marshal(batch)
	for _, secret := range []string{"SECRET", "someone@example.com", "account-uuid-1"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("parsed batch retained %q", secret)
		}
	}
	if !ValidIdentity(identity) || ValidIdentity("account-uuid-1") || AccountIdentity(" ") != "" {
		t.Fatal("account identity validation is wrong")
	}
}

func TestParseOTLPLogsFallsBackToClientRequestID(t *testing.T) {
	payload := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"timeUnixNano":"1790715665248000000","attributes":[
		{"key":"event.name","value":{"stringValue":"api_request"}},{"key":"model","value":{"stringValue":"claude-opus-5-5"}},
		{"key":"client_request_id","value":{"stringValue":"local-1"}}]}]}]}]}`
	batch, err := ParseOTLPLogs([]byte(payload), time.Now())
	if err != nil || len(batch.Requests) != 1 || batch.Requests[0].ID != "client:local-1" || batch.Requests[0].Account != "" ||
		batch.Requests[0].At != time.Unix(0, 1790715665248000000).UTC().Format(time.RFC3339Nano) {
		t.Fatalf("batch = %#v, %v", batch, err)
	}
	received := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	undated := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[
		{"observedTimeUnixNano":"1790715665248000000","attributes":[{"key":"event.name","value":{"stringValue":"api_request"}},
		 {"key":"model","value":{"stringValue":"m"}},{"key":"request_id","value":{"stringValue":"observed"}}]},
		{"attributes":[{"key":"event.name","value":{"stringValue":"api_request"}},
		 {"key":"model","value":{"stringValue":"m"}},{"key":"request_id","value":{"stringValue":"received"}}]}]}]}]}`
	batch, err = ParseOTLPLogs([]byte(undated), received)
	if err != nil || len(batch.Requests) != 2 || batch.Requests[0].At != time.Unix(0, 1790715665248000000).UTC().Format(time.RFC3339Nano) ||
		batch.Requests[1].At != received.Format(time.RFC3339Nano) {
		t.Fatalf("undated batch = %#v, %v", batch, err)
	}
	if _, err = ParseOTLPLogs([]byte("not json"), received); err == nil {
		t.Fatal("invalid payload was accepted")
	}
}

func TestParseStatusLineReadsOnlyCurrentPlanWindows(t *testing.T) {
	now := time.Unix(1790715665, 0).UTC()
	input := `{"session_id":"session-1","cwd":"/secret/path","transcript_path":"/secret/t.jsonl","rate_limits":{
		"five_hour":{"used_percentage":5,"resets_at":1790731800},
		"seven_day":{"used_percentage":64.5,"resets_at":1791043200},
		"spend_limit":{"used_percentage":12,"resets_at":1791043200}}}`
	reading, err := ParseStatusLine([]byte(input), now)
	if err != nil {
		t.Fatal(err)
	}
	want := []Window{{Seconds: 604800, UsedPercent: 64.5, ResetAt: "2026-10-03T16:00:00Z"}, {Seconds: 18000, UsedPercent: 5, ResetAt: "2026-09-30T01:30:00Z"}}
	if reading.SessionID != "session-1" || len(reading.Windows) != 2 || reading.Windows[0] != want[0] || reading.Windows[1] != want[1] {
		t.Fatalf("reading = %#v", reading)
	}
	expired := `{"session_id":"s","rate_limits":{"five_hour":{"used_percentage":90,"resets_at":1790715600}}}`
	if reading, err = ParseStatusLine([]byte(expired), now); err != nil || len(reading.Windows) != 0 {
		t.Fatalf("expired window was kept: %#v", reading)
	}
	if reading, err = ParseStatusLine([]byte(`{"session_id":"api-key-user"}`), now); err != nil || len(reading.Windows) != 0 {
		t.Fatalf("missing rate limits = %#v %v", reading, err)
	}
}

func TestScanHistoryMergesContentBlocksAndSubagents(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "projects", "-Users-someone-project")
	subagents := filepath.Join(project, "session-1", "subagents")
	if err := os.MkdirAll(subagents, 0o700); err != nil {
		t.Fatal(err)
	}
	usage := func(output int) string {
		return `{"input_tokens":10,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000,"output_tokens":` + itoa(output) + `,"output_tokens_details":{"thinking_tokens":7}}`
	}
	main := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"a prompt that mentions \"assistant\" and \"usage\""},"timestamp":"2026-09-29T10:00:00Z"}`,
		`{"type":"assistant","requestId":"req_a","timestamp":"2026-09-29T10:00:02Z","message":{"id":"msg_a","model":"claude-opus-5-5","content":[{"type":"thinking"}],"usage":` + usage(20) + `}}`,
		`{"type":"assistant","requestId":"req_a","timestamp":"2026-09-29T10:00:03Z","message":{"id":"msg_a","model":"claude-opus-5-5","content":[{"type":"text"}],"usage":` + usage(39) + `}}`,
		`{"type":"assistant","timestamp":"2026-09-29T10:00:04Z","message":{"id":"msg_old","model":"claude-opus-5-5","usage":` + usage(5) + `}}`,
		`{"type":"assistant","requestId":"req_synthetic","timestamp":"2026-09-29T10:00:05Z","message":{"model":"<synthetic>","usage":` + usage(0) + `}}`,
		`{"type":"assistant","usage": broken`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(project, "session-1.jsonl"), []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}
	subagent := `{"type":"assistant","requestId":"req_b","timestamp":"2026-09-29T09:00:00Z","message":{"id":"msg_b","model":"claude-haiku-4-5-20251001","usage":` + usage(3) + `}}` + "\n"
	if err := os.WriteFile(filepath.Join(subagents, "agent-1.jsonl"), []byte(subagent), 0o600); err != nil {
		t.Fatal(err)
	}
	history, err := ScanHistory(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if history.FilesScanned != 2 || len(history.Requests) != 3 || history.Models != 2 || history.MalformedRecords != 1 {
		t.Fatalf("history = %#v", history)
	}
	byID := make(map[string]Request)
	for _, request := range history.Requests {
		byID[request.ID] = request
	}
	merged := byID["req_a"]
	if merged.OutputTokens != 39 || merged.InputTokens != 10 || merged.CacheReadTokens != 1000 || merged.CacheCreationTokens != 100 ||
		merged.ThinkingTokens != 7 || merged.At != "2026-09-29T10:00:02Z" {
		t.Fatalf("merged request = %#v", merged)
	}
	if _, ok := byID["message:msg_old"]; !ok {
		t.Fatalf("request without requestId was not keyed by message ID: %#v", byID)
	}
	if history.Requests[0].ID != "req_b" || history.FirstAt != "2026-09-29T09:00:00Z" || history.LastAt != "2026-09-29T10:00:04Z" {
		t.Fatalf("ordering = %#v", history)
	}
	if history.InputTokens != 3*1110 || history.OutputTokens != 39+5+3 {
		t.Fatalf("totals = %d/%d", history.InputTokens, history.OutputTokens)
	}
	if _, err = ScanHistory(context.Background(), t.TempDir()); err == nil {
		t.Fatal("missing projects directory was accepted")
	}
}

func itoa(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

var testIntegration = Integration{HelperCommand: "'/Applications/OpenCDX Router.app/Contents/Resources/router-helper'", Port: 17464}

func TestInstallWrapsStatusLineAndPreservesSettings(t *testing.T) {
	current := []byte(`{
  "permissions": {
    "allow": ["Bash(ls:*)"]
  },
  "statusLine": {
    "type": "command",
    "command": "bash \"$HOME/.claude/statusline.sh\"",
    "padding": 1
  },
  "env": {
    "FOO": "<bar & baz>"
  },
  "theme": "dark"
}
`)
	plan, err := Install(current, testIntegration)
	if err != nil {
		t.Fatal(err)
	}
	if plan.AlreadyInstalled || plan.WrapsStatusLine != `bash "$HOME/.claude/statusline.sh"` || len(plan.Changes) != 3 {
		t.Fatalf("plan = %#v", plan)
	}
	var original map[string]any
	if err = json.Unmarshal(plan.Original, &original); err != nil || original["padding"] != float64(1) {
		t.Fatalf("original status line = %s", plan.Original)
	}
	want := `{
  "permissions": {
    "allow": [
      "Bash(ls:*)"
    ]
  },
  "statusLine": {
    "type": "command",
    "command": "'/Applications/OpenCDX Router.app/Contents/Resources/router-helper' claude-statusline",
    "padding": 1
  },
  "env": {
    "FOO": "<bar & baz>",
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "OTEL_LOGS_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://127.0.0.1:17464/claude/otlp/v1/logs"
  },
  "theme": "dark",
  "otelHeadersHelper": "'/Applications/OpenCDX Router.app/Contents/Resources/router-helper' claude-otel-headers"
}
`
	if string(plan.Settings) != want {
		t.Fatalf("settings =\n%s", plan.Settings)
	}
	if installed, err := Installed(plan.Settings); err != nil || !installed {
		t.Fatalf("installed = %v %v", installed, err)
	}
	again, err := Install(plan.Settings, testIntegration)
	if err != nil || !again.AlreadyInstalled || len(again.Changes) != 0 || again.Original != nil || string(again.Settings) != want {
		t.Fatalf("second install = %#v %v", again, err)
	}
	moved, err := Install(plan.Settings, Integration{HelperCommand: "/opt/router-helper", Port: 17464})
	if err != nil || moved.AlreadyInstalled || moved.Original != nil || !strings.Contains(string(moved.Settings), `"/opt/router-helper claude-statusline"`) {
		t.Fatalf("moved helper = %#v %v", moved, err)
	}

	removed, err := Remove(plan.Settings, plan.Original)
	if err != nil {
		t.Fatal(err)
	}
	restored := `{
  "permissions": {
    "allow": [
      "Bash(ls:*)"
    ]
  },
  "statusLine": {
    "type": "command",
    "command": "bash \"$HOME/.claude/statusline.sh\"",
    "padding": 1
  },
  "env": {
    "FOO": "<bar & baz>"
  },
  "theme": "dark"
}
`
	if string(removed.Settings) != restored || len(removed.Changes) != 3 {
		t.Fatalf("removed =\n%s\n%#v", removed.Settings, removed.Changes)
	}
	if installed, _ := Installed(removed.Settings); installed {
		t.Fatal("removal left OpenCDX entries")
	}
}

func TestInstallIntoEmptySettingsAndRemoveEverything(t *testing.T) {
	plan, err := Install(nil, testIntegration)
	if err != nil || plan.Original != nil || plan.WrapsStatusLine != "" {
		t.Fatalf("plan = %#v %v", plan, err)
	}
	removed, err := Remove(plan.Settings, nil)
	if err != nil || string(removed.Settings) != "{}\n" {
		t.Fatalf("removed = %q %v", removed.Settings, err)
	}
	unchanged, err := Remove(removed.Settings, nil)
	if err != nil || !unchanged.AlreadyInstalled {
		t.Fatalf("second removal = %#v %v", unchanged, err)
	}
}

func TestInstallRefusesUserTelemetrySettings(t *testing.T) {
	for _, current := range []string{
		`{"env":{"OTEL_EXPORTER_OTLP_ENDPOINT":"https://collector.example.com"}}`,
		`{"env":{"CLAUDE_CODE_ENABLE_TELEMETRY":"0"}}`,
		`{"otelHeadersHelper":"/usr/local/bin/my-headers"}`,
	} {
		_, err := Install([]byte(current), testIntegration)
		var conflict *ConflictError
		if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 {
			t.Fatalf("%s: err = %v", current, err)
		}
	}
	for _, current := range []string{`[]`, `{"statusLine":"text"}`, `{"a":1} {"b":2}`, `{"a":`} {
		if _, err := Install([]byte(current), testIntegration); err == nil {
			t.Fatalf("invalid settings %q were accepted", current)
		}
	}
}

func TestShellQuote(t *testing.T) {
	for input, want := range map[string]string{
		"/usr/local/bin/router-helper":       "/usr/local/bin/router-helper",
		"/Applications/OpenCDX Router.app/x": "'/Applications/OpenCDX Router.app/x'",
		"/tmp/it's":                          `'/tmp/it'"'"'s'`,
		"":                                   "''",
	} {
		if got := ShellQuote(input); got != want {
			t.Fatalf("ShellQuote(%q) = %s", input, got)
		}
	}
}
