package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
	"github.com/Dodelidoo-Labs/open-cdx/internal/routing"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
	"github.com/Dodelidoo-Labs/open-cdx/internal/usagehistory"
	site "github.com/Dodelidoo-Labs/open-cdx/web"
)

func TestAPIErrorRedaction(t *testing.T) {
	for _, message := range []string{
		"refresh token rejected", "Bearer abc", "credential failed", "API key denied", "authorization code invalid", "account ID changed",
	} {
		redacted := safeError(errors.New(message))
		if strings.Contains(strings.ToLower(redacted), "token") || strings.Contains(strings.ToLower(redacted), "bearer") || strings.Contains(strings.ToLower(redacted), "api key") || strings.Contains(strings.ToLower(redacted), "account id") {
			t.Fatalf("sensitive error was not redacted: %q", redacted)
		}
	}
}

func TestClientVersionNormalization(t *testing.T) {
	if value := normalizedVersion("0.150.1"); value != "0.150.1" {
		t.Fatalf("valid Codex version changed to %q", value)
	}
	for _, input := range []string{"", "codex-cli 0.150.1", "background", "1.2"} {
		if value := normalizedVersion(input); value != "0.0.0" {
			t.Fatalf("invalid client version %q became %q", input, value)
		}
	}
}

func TestDeviceAcknowledgesCatalogRestart(t *testing.T) {
	registry := routing.NewStatusRegistry()
	registry.Update("device-a", func(status *routing.RouteStatus) {
		status.RestartRequired = true
	})
	server := &Server{status: registry}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/restart-ack", nil)
	request = request.WithContext(context.WithValue(request.Context(), deviceContextKey{}, storage.Device{ID: "device-a"}))
	response := httptest.NewRecorder()
	server.acknowledgeCatalogRestart(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("acknowledgement status = %d", response.Code)
	}
	if registry.Get("device-a").RestartRequired {
		t.Fatal("router restart reminder remained set after acknowledgement")
	}
}

func TestAdminDeletesRejectedDeviceAndRouteStatus(t *testing.T) {
	box, err := secure.NewBox(bytes.Repeat([]byte{0x71}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	enrollment, err := store.CreateEnrollment(context.Background(), "Old Mac")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RejectDevice(context.Background(), enrollment.DeviceID); err != nil {
		t.Fatal(err)
	}
	registry := routing.NewStatusRegistry()
	registry.Update(enrollment.DeviceID, func(status *routing.RouteStatus) {
		status.State, status.Connected = "degraded", false
	})
	form := url.Values{"return_tab": {"devices"}}
	request := httptest.NewRequest(http.MethodPost, "/admin/devices/"+enrollment.DeviceID+"/delete", strings.NewReader(form.Encode()))
	request.SetPathValue("id", enrollment.DeviceID)
	request.SetPathValue("action", "delete")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err = request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	(&Server{store: store, status: registry}).adminDevice(response, request)
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "Device+deleted") {
		t.Fatalf("device deletion response = %d %q", response.Code, response.Header().Get("Location"))
	}
	if devices, listErr := store.Devices(context.Background()); listErr != nil || len(devices) != 0 {
		t.Fatalf("deleted device remains listed: %#v, %v", devices, listErr)
	}
	if status := registry.Get(enrollment.DeviceID); status.State != "connected" || !status.Connected {
		t.Fatalf("stale route status remained after deletion: %#v", status)
	}
}

func TestAdminRevokePermanentlyRemovesApprovedDeviceAndRouteStatus(t *testing.T) {
	box, err := secure.NewBox(bytes.Repeat([]byte{0x72}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	enrollment, err := store.CreateEnrollment(context.Background(), "Retired Mac")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ApproveDevice(context.Background(), enrollment.DeviceID); err != nil {
		t.Fatal(err)
	}
	registry := routing.NewStatusRegistry()
	registry.Update(enrollment.DeviceID, func(status *routing.RouteStatus) {
		status.State, status.Connected = "degraded", false
	})
	form := url.Values{"return_tab": {"devices"}}
	request := httptest.NewRequest(http.MethodPost, "/admin/devices/"+enrollment.DeviceID+"/revoke", strings.NewReader(form.Encode()))
	request.SetPathValue("id", enrollment.DeviceID)
	request.SetPathValue("action", "revoke")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err = request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	(&Server{store: store, status: registry}).adminDevice(response, request)
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "Device+removed") {
		t.Fatalf("device revoke response = %d %q", response.Code, response.Header().Get("Location"))
	}
	if devices, listErr := store.Devices(context.Background()); listErr != nil || len(devices) != 0 {
		t.Fatalf("revoked device remains listed: %#v, %v", devices, listErr)
	}
	if status := registry.Get(enrollment.DeviceID); status.State != "connected" || !status.Connected {
		t.Fatalf("stale route status remained after revoke: %#v", status)
	}
}

func TestFormatIntegerUsesApostropheGrouping(t *testing.T) {
	for value, want := range map[int]string{
		0:         "0",
		12:        "12",
		1999:      "1'999",
		123456789: "123'456'789",
		-42000:    "-42'000",
	} {
		if got := formatInteger(value); got != want {
			t.Fatalf("formatInteger(%d) = %q, want %q", value, got, want)
		}
	}
}

func TestAssetVersionIncludesBuildCommit(t *testing.T) {
	if got := assetVersionFor("1.2.3", "0123456789abcdef"); got != "1.2.3-0123456789ab" {
		t.Fatalf("asset version = %q", got)
	}
}

func TestBrowserTimestampUsesUTCISO8601(t *testing.T) {
	zone := time.FixedZone("test", -3*60*60)
	value := time.Date(2026, time.August, 29, 23, 31, 0, 0, zone)
	if got := browserTimestamp(value); got != "2026-08-30T02:31:00Z" {
		t.Fatalf("browser timestamp = %q", got)
	}
	if got := browserTimestamp(time.Time{}); got != "" {
		t.Fatalf("zero browser timestamp = %q", got)
	}
}

func liveTestServer(t *testing.T) (*Server, *storage.Store) {
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
	templates, err := template.New("site").Funcs(template.FuncMap{"number": formatInteger}).ParseFS(site.Templates, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	return &Server{store: store, templates: templates, sessions: make(map[string]adminSession)}, store
}

func telemetryResponse(t *testing.T, server *Server, etag string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/admin/telemetry", nil)
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response := httptest.NewRecorder()
	server.adminTelemetry(response, request)
	return response
}

func isQuotedETag(value string) bool {
	return len(value) >= 3 && value[0] == '"' && value[len(value)-1] == '"'
}

func TestLiveEndpointsRequireAdminAuthentication(t *testing.T) {
	server, _ := liveTestServer(t)
	handler := server.routes()
	for _, path := range []string{"/admin/telemetry", "/admin/devices/live", "/admin/accounts/live"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/login" {
			t.Fatalf("unauthenticated %s response = %d, location=%q", path, response.Code, response.Header().Get("Location"))
		}
	}
}

func TestAdminTelemetryConditionalETagTracksSuccessfulMutations(t *testing.T) {
	server, store := liveTestServer(t)
	initial := telemetryResponse(t, server, "")
	initialETag := initial.Header().Get("ETag")
	if initial.Code != http.StatusOK || !strings.Contains(initial.Header().Get("Content-Type"), "application/json") || !isQuotedETag(initialETag) {
		t.Fatalf("initial telemetry response = %d, type=%q, etag=%q", initial.Code, initial.Header().Get("Content-Type"), initialETag)
	}
	unchanged := telemetryResponse(t, server, initialETag)
	if unchanged.Code != http.StatusNotModified || unchanged.Body.Len() != 0 {
		t.Fatalf("unchanged telemetry response = %d, body=%q", unchanged.Code, unchanged.Body.String())
	}

	if err := store.RecordUsage(context.Background(), "openai", "gpt-test", "account", 12, 3); err != nil {
		t.Fatal(err)
	}
	recorded := telemetryResponse(t, server, initialETag)
	recordedETag := recorded.Header().Get("ETag")
	if recorded.Code != http.StatusOK || recordedETag == initialETag {
		t.Fatalf("recorded telemetry etag = %q, initial=%q, status=%d", recordedETag, initialETag, recorded.Code)
	}

	replacement := []storage.UsageAggregate{
		{Day: "2026-08-30", Provider: "openai", ModelID: "gpt-test", Routing: storage.UsageRoutingNative, Requests: 1, InputTokens: 10},
		{Day: "2026-08-30", Provider: "openai", ModelID: "gpt-test", Routing: storage.UsageRoutingRouted, Requests: 2, InputTokens: 20},
	}
	if err := store.ReplaceUsage(context.Background(), replacement, storage.UsageReconciliation{ReconciledAt: time.Now(), RowsImported: 2}); err != nil {
		t.Fatal(err)
	}
	replaced := telemetryResponse(t, server, recordedETag)
	replacedETag := replaced.Header().Get("ETag")
	if replaced.Code != http.StatusOK || replacedETag == recordedETag {
		t.Fatalf("replaced telemetry etag = %q, prior=%q, status=%d", replacedETag, recordedETag, replaced.Code)
	}
	var report struct {
		Usage []struct {
			Routing string `json:"routing"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(replaced.Body.Bytes(), &report); err != nil || len(report.Usage) != 2 {
		t.Fatalf("replacement report = %#v, err=%v", report, err)
	}
	routings := map[string]bool{}
	for _, row := range report.Usage {
		routings[row.Routing] = true
	}
	if !routings[storage.UsageRoutingNative] || !routings[storage.UsageRoutingRouted] {
		t.Fatalf("replacement report lost routing classes: %#v", report.Usage)
	}

	if err := store.ResetTelemetry(context.Background()); err != nil {
		t.Fatal(err)
	}
	reset := telemetryResponse(t, server, replacedETag)
	if reset.Code != http.StatusOK || reset.Header().Get("ETag") == replacedETag {
		t.Fatalf("reset telemetry etag = %q, prior=%q, status=%d", reset.Header().Get("ETag"), replacedETag, reset.Code)
	}
}

func TestTelemetryETagChangesAtUTCDayBoundary(t *testing.T) {
	before := telemetryETag("process-seed", 7, time.Date(2026, time.August, 31, 23, 59, 59, 0, time.UTC))
	after := telemetryETag("process-seed", 7, time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC))
	if before == after {
		t.Fatalf("telemetry etag did not change across UTC midnight: %q", before)
	}
}

func TestAdminDevicesLiveConditionalResponsesTrackLifecycle(t *testing.T) {
	server, store := liveTestServer(t)
	request := func(etag string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/admin/devices/live", nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		response := httptest.NewRecorder()
		server.adminDevicesLive(response, req)
		return response
	}
	initial := request("")
	initialETag := initial.Header().Get("ETag")
	unchanged := request(initialETag)
	if initial.Code != http.StatusOK || !isQuotedETag(initialETag) || unchanged.Code != http.StatusNotModified || unchanged.Body.Len() != 0 {
		t.Fatalf("initial/unchanged device responses = %d/%d, body=%q", initial.Code, unchanged.Code, unchanged.Body.String())
	}

	pending, err := store.CreateEnrollment(context.Background(), "New MacBook")
	if err != nil {
		t.Fatal(err)
	}
	created := request(initialETag)
	createdETag := created.Header().Get("ETag")
	if created.Code != http.StatusOK || createdETag == initialETag || !strings.Contains(created.Body.String(), "New MacBook") || !strings.Contains(created.Body.String(), "/approve") {
		t.Fatalf("pending device response = %d, etag=%q, body=%q", created.Code, createdETag, created.Body.String())
	}
	if err = store.ApproveDevice(context.Background(), pending.DeviceID); err != nil {
		t.Fatal(err)
	}
	approved := request(createdETag)
	approvedETag := approved.Header().Get("ETag")
	if approved.Code != http.StatusOK || approvedETag == createdETag || !strings.Contains(approved.Body.String(), "approved") || !strings.Contains(approved.Body.String(), "/revoke") {
		t.Fatalf("approved device response = %d, etag=%q, body=%q", approved.Code, approvedETag, approved.Body.String())
	}

	rejectedEnrollment, err := store.CreateEnrollment(context.Background(), "Rejected Mac")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RejectDevice(context.Background(), rejectedEnrollment.DeviceID); err != nil {
		t.Fatal(err)
	}
	rejected := request(approvedETag)
	rejectedETag := rejected.Header().Get("ETag")
	if rejected.Code != http.StatusOK || rejectedETag == approvedETag || !strings.Contains(rejected.Body.String(), "rejected") || !strings.Contains(rejected.Body.String(), "/delete") {
		t.Fatalf("rejected device response = %d, etag=%q, body=%q", rejected.Code, rejectedETag, rejected.Body.String())
	}
	if err = store.RevokeDevice(context.Background(), pending.DeviceID); err != nil {
		t.Fatal(err)
	}
	removed := request(rejectedETag)
	if removed.Code != http.StatusOK || removed.Header().Get("ETag") == rejectedETag || strings.Contains(removed.Body.String(), "New MacBook") {
		t.Fatalf("removed device response = %d, etag=%q, body=%q", removed.Code, removed.Header().Get("ETag"), removed.Body.String())
	}
	if strings.Contains(created.Body.String(), pending.EnrollmentSecret) {
		t.Fatal("devices live response exposed an enrollment secret")
	}
}

func TestAdminAccountsLiveIsConditionalLightweightAndPrivacyMinimal(t *testing.T) {
	server, store := liveTestServer(t)
	resetAt := time.Now().UTC().Add(5 * 24 * time.Hour).Truncate(time.Second)
	rawQuota := json.RawMessage(fmt.Sprintf(`{"secret_quota_marker":"RAW_QUOTA_SECRET","rate_limit":{"allowed":true,"primary_window":{"used_percent":20,"limit_window_seconds":604800,"reset_at":%d}},"additional_rate_limits":[{"limit_name":"Codex Spark","rate_limit":{"allowed":true,"primary_window":{"used_percent":25,"reset_at":1788220800}}}]}`, resetAt.Unix()))
	account, _, err := store.PutAccount(context.Background(), storage.AccountInput{
		Credential: storage.OpenAICredential{
			AccountID: "RAW_ACCOUNT_ID", AccessToken: "RAW_ACCESS_TOKEN", RefreshToken: "RAW_REFRESH_TOKEN", IDToken: "RAW_ID_TOKEN", ExpiresAt: time.Now().Add(time.Hour),
		},
		MaskedEmail: "a***@example.com", Plan: "plus", Status: "ready", QuotaUsedPercent: 20,
		QuotaResetAt: resetAt, ResetCredits: 2,
		RawQuota: rawQuota, RawCatalogSnapshot: json.RawMessage(`{"raw_catalog_marker":"RAW_CATALOG_SECRET"}`), EntitledModels: []string{"SECRET_MODEL"},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	request := func(etag string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/admin/accounts/live", nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		response := httptest.NewRecorder()
		server.adminAccountsLive(response, req)
		return response
	}
	initial := request("")
	initialETag := initial.Header().Get("ETag")
	if initial.Code != http.StatusOK || !isQuotedETag(initialETag) || !strings.Contains(initial.Header().Get("Content-Type"), "application/json") || !strings.Contains(initial.Body.String(), `"remaining":80`) || !strings.Contains(initial.Body.String(), `"label":"Weekly"`) || !strings.Contains(initial.Body.String(), `"pace_status":"on_pace"`) || !strings.Contains(initial.Body.String(), "Codex Spark") {
		t.Fatalf("initial account response = %d, etag=%q, body=%q", initial.Code, initialETag, initial.Body.String())
	}
	for _, secret := range []string{"RAW_ACCOUNT_ID", "RAW_ACCESS_TOKEN", "RAW_REFRESH_TOKEN", "RAW_ID_TOKEN", "RAW_QUOTA_SECRET", "RAW_CATALOG_SECRET", "SECRET_MODEL", "credential", "access_token", "refresh_token", "id_token", "raw_quota", "raw_catalog"} {
		if strings.Contains(strings.ToLower(initial.Body.String()), strings.ToLower(secret)) {
			t.Fatalf("accounts live response exposed %q: %s", secret, initial.Body.String())
		}
	}
	unchanged := request(initialETag)
	if unchanged.Code != http.StatusNotModified || unchanged.Body.Len() != 0 {
		t.Fatalf("unchanged account response = %d, body=%q", unchanged.Code, unchanged.Body.String())
	}
	updatedReset := resetAt.Add(24 * time.Hour)
	updatedRaw := json.RawMessage(fmt.Sprintf(`{"quota_mutation_secret":"HIDDEN","rate_limit":{"allowed":true,"primary_window":{"used_percent":80,"limit_window_seconds":604800,"reset_at":%d}}}`, updatedReset.Unix()))
	if err = store.UpdateAccountQuota(context.Background(), account.ID, "pro", 80, updatedReset, 1, updatedRaw); err != nil {
		t.Fatal(err)
	}
	quotaChanged := request(initialETag)
	quotaETag := quotaChanged.Header().Get("ETag")
	if quotaChanged.Code != http.StatusOK || quotaETag == initialETag || !strings.Contains(quotaChanged.Body.String(), `"remaining":20`) || strings.Contains(quotaChanged.Body.String(), "HIDDEN") {
		t.Fatalf("quota update response = %d, etag=%q, body=%q", quotaChanged.Code, quotaETag, quotaChanged.Body.String())
	}
	if err = store.SetAccountStatus(context.Background(), account.ID, "error", "quota refresh failed"); err != nil {
		t.Fatal(err)
	}
	statusChanged := request(quotaETag)
	if statusChanged.Code != http.StatusOK || statusChanged.Header().Get("ETag") == quotaETag || !strings.Contains(statusChanged.Body.String(), "quota refresh failed") || !strings.Contains(statusChanged.Body.String(), `"healthy":false`) {
		t.Fatalf("status update response = %d, etag=%q, body=%q", statusChanged.Code, statusChanged.Header().Get("ETag"), statusChanged.Body.String())
	}
}

func TestSafeAccountsExposeOnlyReportedQuotaWindows(t *testing.T) {
	server, store := liveTestServer(t)
	resetAt := time.Now().UTC().Add(6 * 24 * time.Hour)
	rawQuota := json.RawMessage(fmt.Sprintf(`{
		"private_marker":"HIDDEN_RAW_QUOTA",
		"rate_limit":{"allowed":true,"primary_window":null,"secondary_window":{"used_percent":7,"limit_window_seconds":604800,"reset_at":%d}}
	}`, resetAt.Unix()))
	if _, _, err := store.PutAccount(context.Background(), storage.AccountInput{
		Credential: storage.OpenAICredential{
			AccountID: "account-a", AccessToken: "access", RefreshToken: "refresh", IDToken: "id", ExpiresAt: time.Now().Add(time.Hour),
		},
		MaskedEmail: "a***@example.com", Plan: "pro", Status: "ready", QuotaUsedPercent: 7,
		QuotaResetAt: resetAt, RawQuota: rawQuota,
	}, false); err != nil {
		t.Fatal(err)
	}

	accounts, err := server.safeAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(accounts)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	if !strings.Contains(body, `"quota_windows":[{"label":"Weekly","remaining":93`) {
		t.Fatalf("reported weekly window was not exposed: %s", body)
	}
	for _, unwanted := range []string{"5 hours", "HIDDEN_RAW_QUOTA", "private_marker", "access", "refresh"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("safe account payload exposed or invented %q: %s", unwanted, body)
		}
	}
}

func TestLiveQuotaWindowsRetainZeroPercentPaceMarker(t *testing.T) {
	encoded, err := json.Marshal(liveQuotaWindows([]quotaWindowState{{
		Label: "5 hours", Remaining: 4, PaceStatus: "too_fast", PaceMarkerPercent: 0,
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"pace_marker_percent":0`) {
		t.Fatalf("zero-percent pace marker was omitted: %s", encoded)
	}
}

func TestDashboardTemplateRendersRedesignedSections(t *testing.T) {
	templates, err := template.New("site").Funcs(template.FuncMap{"number": formatInteger}).ParseFS(site.Templates, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	page := dashboardPage{
		Message: "Settings saved", RepositoryURL: "https://github.com/Dodelidoo-Labs/open-cdx",
		AssetVersion:   "1.0.0-test",
		CurrentVersion: "1.0.0", LatestVersion: "1.1.0", UpdateAvailable: true,
		NearestResetDate: "Aug 30", NearestResetTime: "02:31", NearestResetAt: "2026-08-30T02:31:00Z",
		ProvidersChecked: "Aug 30 02:31", ProvidersCheckedAt: "2026-08-30T02:31:00Z",
		Accounts: []accountView{
			{
				ID: "account", MaskedEmail: "a***@example.com", Plan: "pro", Status: "ready", Primary: true,
				CodexReset: "Aug 30 · 02:31", CodexResetAt: "2026-08-30T02:31:00Z",
				VisibleModels: []string{"gpt-test"}, MoreModels: []string{"gpt-test-2"},
				Quotas: []quotaView{
					{Name: "Codex", Windows: []quotaWindowView{{Label: "Weekly", Reset: "Aug 30 · 02:31", ResetAt: "2026-08-30T02:31:00Z", Remaining: 80, PaceStatus: "on_pace", PaceMarkerPercent: 60, PaceBufferPercent: 20, PaceDifferencePercent: 20}}},
					{Name: "Codex Spark", Windows: []quotaWindowView{{Label: "Allowance", Remaining: 60}}, Spark: true},
				},
			},
			{ID: "fallback", MaskedEmail: "b***@example.com", Plan: "plus", Status: "ready"},
		},
		Providers: []providerView{
			{Name: "openrouter", DisplayName: "OpenRouter", Description: "OpenRouter API", Health: "healthy", HasCredential: true, Updated: "Aug 30 02:31", UpdatedAt: "2026-08-30T02:31:00Z"},
			{Name: "ollama", DisplayName: "Ollama", Description: "Local or remote Ollama API", BaseURL: "http://192.168.1.20:11434", Health: "healthy", AllowHTTP: true},
		},
		Devices: []deviceView{
			{ID: "device", Name: "MacBook Pro", Status: "approved", LastSeen: "Aug 30 02:31", LastSeenAt: "2026-08-30T02:31:00Z", Laptop: true},
			{ID: "retired", Name: "Old Mac", Status: "rejected"},
		},
		Models:              []modelView{{Provider: "openrouter", Model: "example/model", State: "available"}},
		Conflicts:           []conflictView{{Model: "gpt-test", Detail: "definitions differ"}},
		AvailableModelCount: 1,
	}
	var output strings.Builder
	if err = templates.ExecuteTemplate(&output, "dashboard.html", page); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		`data-tab="home"`, `data-tab="accounts"`, `data-tab="providers"`, `data-tab="devices"`, `data-tab="catalog"`,
		`class="sidebar"`, `data-telemetry`, `data-telemetry-range`, `data-usage-chart="tokens"`, `model-breakdown-section`,
		`data-custom-range`, `role="dialog"`, `data-flash-dismiss`,
		`class="rail-actions"`, `data-theme-toggle`, `aria-label="Sign out"`, `material-symbols-outlined`,
		`href="com.dodelidoo.opencdx://oauth/openai/start">Connect account`, `provider-config-trigger`, `Refresh catalog`,
		`/admin/telemetry/reset`, `Reset telemetry…`, `name="allow_http" checked`, `HTTP allowed`,
		`account-order-controls`, `account-primary-star`, `material-symbols-filled`, `/admin/accounts/fallback/primary`,
		`data-account-list`, `data-account-drag`, `/admin/accounts/reorder`, `data-accounts-live`, `data-devices-live`,
		`class="project-version"`, `href="https://github.com/Dodelidoo-Labs/open-cdx"`, `class="update-current">v1.0.0`, `class="update-latest"> → v1.1.0`,
		`/assets/opencdx-router-logo.png?v=1.0.0-test`, `/assets/favicon-32x32.png?v=1.0.0-test`,
		`/admin/devices/device/revoke`, `/admin/devices/retired/delete`,
		`datetime="2026-08-30T02:31:00Z"`, `data-local-datetime`, `data-local-date`, `data-local-clock`,
		"[hidden]{display:none!important}", "Codex Spark", "On pace", "quota-pace-marker", "gpt-test-2", `data-sort="provider"`, `data-sort="model"`, `data-sort="state"`,
	} {
		if !strings.Contains(output.String(), marker) {
			t.Fatalf("dashboard is missing %q", marker)
		}
	}
	if strings.Contains(output.String(), "cost") || strings.Contains(output.String(), "price") {
		t.Fatal("dashboard still exposes removed price estimation")
	}
	if strings.Contains(output.String(), "Router online") || strings.Contains(output.String(), "session active") {
		t.Fatal("dashboard still exposes the removed router status footer")
	}
	if strings.Contains(output.String(), "<svg") {
		t.Fatal("dashboard template still embeds hand-authored SVG icons")
	}
	if strings.Contains(output.String(), `class="account-detail-label"`) || strings.Contains(output.String(), `>Make primary</button>`) {
		t.Fatal("dashboard still renders removed account-row labels or the text primary action")
	}
	headerEnd := strings.Index(output.String(), "</header>")
	if headerEnd < 0 {
		t.Fatal("dashboard is missing its header")
	}
	header := output.String()[:headerEnd]
	for _, removed := range []string{"Connect account", `data-refresh`, `data-theme-toggle`, `/admin/logout`} {
		if strings.Contains(header, removed) {
			t.Fatalf("dashboard header still contains %q", removed)
		}
	}
}

func TestDashboardJavaScriptIsServed(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/assets/dashboard.js", nil)
	response := httptest.NewRecorder()
	(&Server{}).routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("dashboard asset status = %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/javascript") {
		t.Fatalf("dashboard asset content type = %q", contentType)
	}
	bundle := response.Body.String()
	for _, marker := range []string{"data-sort-table", "pill.hidden = false", "formatNumber", "renderUsageChart(range, report, mode, grouping)", "data-flash-dismiss", "prepareSeriesColors", "data-group-mode", "const visible = ordered.map", "moveRowAtY", "data-account-order-form", `time[data-local-datetime]`, `time[data-local-date]`, `time[data-local-clock]`, "/admin/telemetry", "/admin/devices/live", "/admin/accounts/live", "If-None-Match", "visibilitychange", "AbortController", "runLiveRefresh"} {
		if !strings.Contains(bundle, marker) {
			t.Fatalf("dashboard behavior bundle is missing %q", marker)
		}
	}
	if strings.Contains(bundle, ".toLocaleString(") {
		t.Fatal("dashboard behavior bundle still delegates numeric grouping to the browser locale")
	}
	if strings.Contains(bundle, "setInterval(") {
		t.Fatal("dashboard live refresh must use recursive timeouts, not setInterval")
	}
}

func TestAdminAccountOrderPersistsFallbackPriority(t *testing.T) {
	box, err := secure.NewBox(bytes.Repeat([]byte{0x39}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	create := func(stable string) storage.Account {
		account, _, createErr := store.PutAccount(context.Background(), storage.AccountInput{
			Credential: storage.OpenAICredential{
				AccountID: stable, AccessToken: "access-" + stable, RefreshToken: "refresh-" + stable,
				IDToken: "id-" + stable, ExpiresAt: time.Now().Add(time.Hour),
			},
			MaskedEmail: stable + "@masked", Plan: "plus", Status: "ready", EntitledModels: []string{"gpt-test"},
		}, false)
		if createErr != nil {
			t.Fatal(createErr)
		}
		return account
	}
	first, second, third := create("first"), create("second"), create("third")
	form := url.Values{
		"account_id": {third.ID, first.ID, second.ID},
		"return_tab": {"accounts"},
	}
	request := httptest.NewRequest(http.MethodPost, "/admin/accounts/reorder", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err = request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	(&Server{store: store}).adminAccountOrder(response, request)
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "#accounts") {
		t.Fatalf("account reorder response = %d %q", response.Code, response.Header().Get("Location"))
	}
	accounts, err := store.Accounts(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	wanted := []string{third.ID, first.ID, second.ID}
	for index, account := range accounts {
		if account.ID != wanted[index] || account.Primary != (index == 0) {
			t.Fatalf("account %d = %s, want %s", index, account.ID, wanted[index])
		}
	}
}

func TestDashboardPresentationAssetsAreServed(t *testing.T) {
	for _, test := range []struct {
		path, contentType, marker string
	}{
		{path: "/assets/dashboard.css", contentType: "text/css", marker: "--activity-4: #56d364"},
		{path: "/assets/material-symbols-outlined.woff2", contentType: "font/woff2"},
		{path: "/assets/opencdx-router-logo.png", contentType: "image/png"},
		{path: "/assets/favicon-32x32.png", contentType: "image/png"},
		{path: "/assets/favicon-16x16.png", contentType: "image/png"},
		{path: "/assets/apple-touch-icon.png", contentType: "image/png"},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		response := httptest.NewRecorder()
		(&Server{}).routes().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("asset %s status = %d", test.path, response.Code)
		}
		if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, test.contentType) {
			t.Fatalf("asset %s content type = %q", test.path, contentType)
		}
		if test.marker != "" && !strings.Contains(response.Body.String(), test.marker) {
			t.Fatalf("asset %s is missing %q", test.path, test.marker)
		}
	}
}

func TestRedirectMessagePreservesSelectedTab(t *testing.T) {
	for _, test := range []struct {
		name, submitted, expected string
	}{
		{name: "valid tab", submitted: "providers", expected: "#providers"},
		{name: "invalid tab", submitted: "unexpected", expected: "#home"},
	} {
		t.Run(test.name, func(t *testing.T) {
			form := url.Values{"return_tab": {test.submitted}}
			request := httptest.NewRequest(http.MethodPost, "/admin/refresh", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			redirectMessage(response, request, "updated", false)
			if location := response.Header().Get("Location"); !strings.HasSuffix(location, test.expected) {
				t.Fatalf("redirect location = %q, expected suffix %q", location, test.expected)
			}
		})
	}
}

func TestDeviceCanReconcilePrivacyMinimalUsageSnapshot(t *testing.T) {
	box, err := secure.NewBox(bytes.Repeat([]byte{0x52}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	enrollment, err := store.CreateEnrollment(context.Background(), "History Mac")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ApproveDevice(context.Background(), enrollment.DeviceID); err != nil {
		t.Fatal(err)
	}
	approved, err := store.EnrollmentStatus(context.Background(), enrollment.DeviceID, enrollment.EnrollmentSecret)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := usagehistory.Snapshot{
		Version: usagehistory.SnapshotVersion, GeneratedAt: "2026-08-28T18:00:00Z",
		FilesScanned: 3, EventsImported: 2,
		Rows: []usagehistory.Row{{
			Day: "2026-08-28", Provider: "openai", Model: "gpt-test", Routing: usagehistory.RoutingNative, Requests: 2,
			InputTokens: 100, CachedInputTokens: 25, OutputTokens: 30, ReasoningOutputTokens: 10,
		}},
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store}
	handler := server.device(http.HandlerFunc(server.reconcileUsage))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/telemetry/reconcile", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+approved.DeviceToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("reconciliation status = %d, body=%s", response.Code, response.Body.String())
	}
	usage, err := store.Usage(context.Background(), time.Time{})
	if err != nil || len(usage) != 1 || usage[0].Source != storage.UsageSourceReconciled || usage[0].Routing != storage.UsageRoutingNative || usage[0].CachedInputTokens != 25 || usage[0].ReasoningOutputTokens != 10 {
		t.Fatalf("reconciled usage = %#v, %v", usage, err)
	}
	telemetryRequest := httptest.NewRequest(http.MethodGet, "/admin/telemetry", nil)
	telemetryResponse := httptest.NewRecorder()
	server.adminTelemetry(telemetryResponse, telemetryRequest)
	if telemetryResponse.Code != http.StatusOK {
		t.Fatalf("telemetry status = %d, body=%s", telemetryResponse.Code, telemetryResponse.Body.String())
	}
	var report struct {
		Usage []struct {
			Source  string `json:"source"`
			Routing string `json:"routing"`
		} `json:"usage"`
		Reconciliation *struct {
			ReconciledAt   string `json:"reconciled_at"`
			EventsImported int    `json:"events_imported"`
		} `json:"reconciliation"`
	}
	if err = json.Unmarshal(telemetryResponse.Body.Bytes(), &report); err != nil || len(report.Usage) != 1 || report.Usage[0].Source != storage.UsageSourceReconciled || report.Usage[0].Routing != storage.UsageRoutingNative || report.Reconciliation == nil || report.Reconciliation.ReconciledAt == "" || report.Reconciliation.EventsImported != 2 {
		t.Fatalf("telemetry reconciliation metadata = %#v, %v", report, err)
	}

	// The strict wire schema prevents conversation-shaped fields from being
	// accepted accidentally in a future helper implementation.
	withPrompt := bytes.TrimSuffix(body, []byte("}"))
	withPrompt = append(withPrompt, []byte(`,"prompts":["must never be accepted"]}`)...)
	request = httptest.NewRequest(http.MethodPost, "/api/v1/telemetry/reconcile", bytes.NewReader(withPrompt))
	request.Header.Set("Authorization", "Bearer "+approved.DeviceToken)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("conversation-shaped payload status = %d", response.Code)
	}
}

func TestDeviceTelemetryResetRequiresAuthentication(t *testing.T) {
	box, err := secure.NewBox(bytes.Repeat([]byte{0x53}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	enrollment, err := store.CreateEnrollment(context.Background(), "Reset Mac")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ApproveDevice(context.Background(), enrollment.DeviceID); err != nil {
		t.Fatal(err)
	}
	approved, err := store.EnrollmentStatus(context.Background(), enrollment.DeviceID, enrollment.EnrollmentSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ReplaceUsage(context.Background(), []storage.UsageAggregate{{
		Day: "2026-08-30", Provider: "openai", ModelID: "gpt-test", Routing: storage.UsageRoutingNative,
		Requests: 1, InputTokens: 10,
	}}, storage.UsageReconciliation{ReconciledAt: time.Now(), FilesScanned: 1, EventsImported: 1, RowsImported: 1}); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: store}
	handler := server.device(http.HandlerFunc(server.resetTelemetry))
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "/api/v1/telemetry/reset", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated reset status = %d", unauthenticated.Code)
	}
	if usage, usageErr := store.Usage(context.Background(), time.Time{}); usageErr != nil || len(usage) != 1 {
		t.Fatalf("unauthenticated reset changed telemetry: %#v, %v", usage, usageErr)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/telemetry/reset", nil)
	request.Header.Set("Authorization", "Bearer "+approved.DeviceToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("authenticated reset status = %d, body=%s", response.Code, response.Body.String())
	}
	if usage, usageErr := store.Usage(context.Background(), time.Time{}); usageErr != nil || len(usage) != 0 {
		t.Fatalf("telemetry remains after authenticated reset: %#v, %v", usage, usageErr)
	}
	if _, metadataErr := store.UsageReconciliation(context.Background()); !errors.Is(metadataErr, storage.ErrNotFound) {
		t.Fatalf("reconciliation metadata remains after reset: %v", metadataErr)
	}
}

func TestAdminOllamaAllowHTTPIsExplicitAndPersistent(t *testing.T) {
	box, err := secure.NewBox(bytes.Repeat([]byte{0x54}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := &Server{store: store}

	submit := func(values url.Values) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/admin/providers/ollama", strings.NewReader(values.Encode()))
		request.SetPathValue("name", "ollama")
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		server.adminProvider(response, request)
		return response
	}
	response := submit(url.Values{
		"base_url": {"http://192.168.1.20:11434"}, "allow_http": {"on"}, "return_tab": {"providers"},
	})
	if response.Code != http.StatusSeeOther || strings.Contains(response.Header().Get("Location"), "error=1") {
		t.Fatalf("explicit HTTP opt-in response = %d %q", response.Code, response.Header().Get("Location"))
	}
	provider, err := store.Provider(context.Background(), "ollama", false)
	if err != nil || !provider.AllowHTTP() {
		t.Fatalf("Allow HTTP was not persisted: %#v, %v", provider, err)
	}

	response = submit(url.Values{"base_url": {"http://192.168.1.20:11434"}, "return_tab": {"providers"}})
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "error=1") {
		t.Fatalf("unchecked remote HTTP response = %d %q", response.Code, response.Header().Get("Location"))
	}
	provider, err = store.Provider(context.Background(), "ollama", false)
	if err != nil || !provider.AllowHTTP() {
		t.Fatalf("rejected update changed the persisted policy: %#v, %v", provider, err)
	}
}
