package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/claudecode"
	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
	"github.com/Dodelidoo-Labs/open-cdx/internal/routing"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
	"github.com/Dodelidoo-Labs/open-cdx/internal/usagehistory"
)

func claudeTestServer(t *testing.T) (*Server, *storage.Store, string, string) {
	t.Helper()
	box, err := secure.NewBox(bytes.Repeat([]byte{0x63}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	enrollment, err := store.CreateEnrollment(ctx, "Claude Mac")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ApproveDevice(ctx, enrollment.DeviceID); err != nil {
		t.Fatal(err)
	}
	approved, err := store.EnrollmentStatus(ctx, enrollment.DeviceID, enrollment.EnrollmentSecret)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{store: store, status: routing.NewStatusRegistry()}, store, enrollment.DeviceID, approved.DeviceToken
}

func postClaudeReport(t *testing.T, server *Server, token string, report any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/claude/telemetry", bytes.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	server.device(http.HandlerFunc(server.claudeTelemetry)).ServeHTTP(response, request)
	return response
}

func TestClaudeTelemetryStoresUsageAndAllowance(t *testing.T) {
	server, store, deviceID, token := claudeTestServer(t)
	now := time.Now().UTC()
	identity := claudecode.AccountIdentity("account-uuid")
	report := claudecode.Report{
		Version: claudecode.ReportVersion, Source: claudecode.SourceLive,
		Accounts: []claudecode.Account{{Identity: identity, MaskedEmail: "s***e@e***.com"}},
		Requests: []claudecode.Request{{
			ID: "req_1", At: now.Add(-time.Minute).Format(time.RFC3339Nano), Model: "claude-opus-5-5", Account: identity,
			InputTokens: 10, CacheReadTokens: 1000, CacheCreationTokens: 100, OutputTokens: 20,
		}},
		Allowances: []claudecode.Allowance{{Account: identity, ObservedAt: now.Format(time.RFC3339Nano), Windows: []claudecode.Window{
			{Seconds: 18000, UsedPercent: 9, ResetAt: now.Add(2 * time.Hour).Format(time.RFC3339)},
			{Seconds: 604800, UsedPercent: 64, ResetAt: now.Add(72 * time.Hour).Format(time.RFC3339)},
		}}},
	}
	if response := postClaudeReport(t, server, "", report); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
	response := postClaudeReport(t, server, token, report)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var result claudecode.Result
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.RequestsAdded != 1 || result.AllowancesRecorded != 1 {
		t.Fatalf("result = %#v %v", result, err)
	}
	if response = postClaudeReport(t, server, token, report); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"requests_already_recorded":1`) {
		t.Fatalf("replayed report = %d %s", response.Code, response.Body.String())
	}
	usage, err := store.Usage(context.Background(), time.Time{})
	if err != nil || len(usage) != 1 {
		t.Fatalf("usage = %#v %v", usage, err)
	}
	if row := usage[0]; row.Provider != storage.ProviderClaudeCode || row.DeviceID != deviceID || row.Source != storage.UsageSourceRouted ||
		row.InputTokens != 1110 || row.CachedInputTokens != 1000 || row.CacheWriteInputTokens != 100 || row.OutputTokens != 20 || row.AccountID == "" {
		t.Fatalf("usage row = %#v", row)
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/v1/device/status", nil)
	statusRequest.Header.Set("Authorization", "Bearer "+token)
	statusResponse := httptest.NewRecorder()
	server.device(http.HandlerFunc(server.deviceStatus)).ServeHTTP(statusResponse, statusRequest)
	var status struct {
		ClaudeAccounts []struct {
			MaskedEmail  string `json:"masked_email"`
			ObservedAt   string `json:"observed_at"`
			QuotaWindows []struct {
				Label      string  `json:"label"`
				Remaining  float64 `json:"remaining"`
				PaceStatus string  `json:"pace_status"`
			} `json:"quota_windows"`
		} `json:"claude_accounts"`
	}
	if err = json.Unmarshal(statusResponse.Body.Bytes(), &status); err != nil || len(status.ClaudeAccounts) != 1 {
		t.Fatalf("device status = %s %v", statusResponse.Body.String(), err)
	}
	account := status.ClaudeAccounts[0]
	if account.MaskedEmail != "s***e@e***.com" || account.ObservedAt == "" || len(account.QuotaWindows) != 2 ||
		account.QuotaWindows[0].Label != "Weekly" || account.QuotaWindows[0].Remaining != 36 ||
		account.QuotaWindows[1].Label != "5 hours" || account.QuotaWindows[1].Remaining != 91 || account.QuotaWindows[1].PaceStatus == "" {
		t.Fatalf("Claude account view = %#v", account)
	}
}

func TestClaudeTelemetryHistoryAndDeviceAttribution(t *testing.T) {
	server, store, _, token := claudeTestServer(t)
	now := time.Now().UTC()
	history := claudecode.Report{Version: claudecode.ReportVersion, Source: claudecode.SourceHistory, Requests: []claudecode.Request{
		{ID: "req_old", At: now.Add(-48 * time.Hour).Format(time.RFC3339Nano), Model: "claude-haiku-4-5", InputTokens: 5, OutputTokens: 2, ThinkingTokens: 1},
	}}
	if response := postClaudeReport(t, server, token, history); response.Code != http.StatusOK {
		t.Fatalf("history status = %d %s", response.Code, response.Body.String())
	}
	usage, _ := store.Usage(context.Background(), time.Time{})
	if len(usage) != 1 || usage[0].Source != storage.UsageSourceReconciled || usage[0].AccountID != "" || usage[0].ReasoningOutputTokens != 1 {
		t.Fatalf("history usage = %#v", usage)
	}
	// Without OpenTelemetry account attributes, the reading belongs to this Mac.
	anonymous := claudecode.Report{Version: claudecode.ReportVersion, Source: claudecode.SourceLive, Allowances: []claudecode.Allowance{{
		ObservedAt: now.Format(time.RFC3339Nano), Windows: []claudecode.Window{{Seconds: 18000, UsedPercent: 5, ResetAt: now.Add(time.Hour).Format(time.RFC3339)}},
	}}}
	if response := postClaudeReport(t, server, token, anonymous); response.Code != http.StatusOK {
		t.Fatalf("anonymous status = %d %s", response.Code, response.Body.String())
	}
	accounts, err := store.ClaudeAccounts(context.Background())
	if err != nil || len(accounts) != 1 || accounts[0].MaskedEmail != "" || accounts[0].LastDeviceName != "Claude Mac" {
		t.Fatalf("accounts = %#v %v", accounts, err)
	}
	if views := claudeAccountViews(accounts, now); views[0]["masked_email"] != "Claude on Claude Mac" {
		t.Fatalf("views = %#v", views)
	}
	if views := claudeAccountViews(accounts, now.Add(2*time.Hour)); views[0]["quota_windows"].([]accountLiveQuotaWindow)[0].Remaining != 100 {
		t.Fatalf("expired window view = %#v", views)
	}
}

func TestClaudeTelemetryRejectsInvalidReports(t *testing.T) {
	server, _, _, token := claudeTestServer(t)
	now := time.Now().UTC()
	identity := claudecode.AccountIdentity("account")
	valid := func() claudecode.Report {
		return claudecode.Report{Version: claudecode.ReportVersion, Source: claudecode.SourceLive,
			Accounts: []claudecode.Account{{Identity: identity}},
			Requests: []claudecode.Request{{ID: "req", At: now.Format(time.RFC3339Nano), Model: "claude-opus-5-5", Account: identity}}}
	}
	cases := map[string]func(*claudecode.Report){
		"version":          func(r *claudecode.Report) { r.Version = 2 },
		"source":           func(r *claudecode.Report) { r.Source = "proxy" },
		"future":           func(r *claudecode.Report) { r.Requests[0].At = now.Add(time.Hour).Format(time.RFC3339Nano) },
		"duplicate":        func(r *claudecode.Report) { r.Requests = append(r.Requests, r.Requests[0]) },
		"unknown account":  func(r *claudecode.Report) { r.Requests[0].Account = claudecode.AccountIdentity("other") },
		"raw identity":     func(r *claudecode.Report) { r.Accounts[0].Identity = "account-uuid" },
		"negative tokens":  func(r *claudecode.Report) { r.Requests[0].OutputTokens = -1 },
		"model whitespace": func(r *claudecode.Report) { r.Requests[0].Model = " claude" },
		"history allowance": func(r *claudecode.Report) {
			r.Source = claudecode.SourceHistory
			r.Allowances = []claudecode.Allowance{{ObservedAt: now.Format(time.RFC3339Nano), Windows: []claudecode.Window{{Seconds: 18000, ResetAt: now.Format(time.RFC3339)}}}}
		},
		"percent": func(r *claudecode.Report) {
			r.Allowances = []claudecode.Allowance{{ObservedAt: now.Format(time.RFC3339Nano), Windows: []claudecode.Window{{Seconds: 18000, UsedPercent: 101, ResetAt: now.Add(time.Hour).Format(time.RFC3339)}}}}
		},
	}
	for name, mutate := range cases {
		report := valid()
		mutate(&report)
		if response := postClaudeReport(t, server, token, report); response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d %s", name, response.Code, response.Body.String())
		}
	}
	unknownField := httptest.NewRequest(http.MethodPost, "/api/v1/claude/telemetry", strings.NewReader(`{"version":1,"source":"live","prompt":"x"}`))
	unknownField.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	server.device(http.HandlerFunc(server.claudeTelemetry)).ServeHTTP(response, unknownField)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", response.Code)
	}
}

func TestCodexHistoryCannotUseReservedClaudeProvider(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	snapshot := usagehistory.Snapshot{
		Version: usagehistory.SnapshotVersion, GeneratedAt: now.Format(time.RFC3339), FilesScanned: 1, EventsImported: 1,
		Rows: []usagehistory.Row{{Day: "2026-09-29", Provider: storage.ProviderClaudeCode, Model: "m", Routing: usagehistory.RoutingNative, Requests: 1}},
	}
	if _, err := validatedHistorySnapshot(snapshot, now); err == nil {
		t.Fatal("Codex history used the reserved Claude Code provider")
	}
}
