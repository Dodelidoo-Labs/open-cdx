package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestDashboardTemplateRendersRedesignedSections(t *testing.T) {
	templates, err := template.New("site").Funcs(template.FuncMap{"number": formatInteger}).ParseFS(site.Templates, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	page := dashboardPage{
		Message: "Settings saved", RepositoryURL: "https://github.com/Dodelidoo-Labs/open-cdx",
		CurrentVersion: "1.0.0", LatestVersion: "1.1.0", UpdateAvailable: true,
		NearestResetDate: "Aug 30", NearestResetTime: "02:31", NearestResetAt: "2026-08-30T02:31:00Z",
		ProvidersChecked: "Aug 30 02:31", ProvidersCheckedAt: "2026-08-30T02:31:00Z",
		Accounts: []accountView{
			{
				ID: "account", MaskedEmail: "a***@example.com", Plan: "pro", Status: "ready", Primary: true,
				VisibleModels: []string{"gpt-test"}, MoreModels: []string{"gpt-test-2"},
				Quotas: []quotaView{{Name: "Codex", Reset: "Aug 30 · 02:31", ResetAt: "2026-08-30T02:31:00Z", Remaining: 80}, {Name: "Codex Spark", Remaining: 60, Spark: true}},
			},
			{ID: "fallback", MaskedEmail: "b***@example.com", Plan: "plus", Status: "ready"},
		},
		Providers: []providerView{{Name: "openrouter", DisplayName: "OpenRouter", Description: "OpenRouter API", Health: "healthy", HasCredential: true, Updated: "Aug 30 02:31", UpdatedAt: "2026-08-30T02:31:00Z"}},
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
		`href="opencdx://oauth/openai/start">Connect account`, `provider-config-trigger`, `Refresh catalog`,
		`account-order-controls`, `account-primary-star`, `material-symbols-filled`, `/admin/accounts/fallback/primary`,
		`data-account-list`, `data-account-drag`, `/admin/accounts/reorder`,
		`class="project-version"`, `href="https://github.com/Dodelidoo-Labs/open-cdx"`, `class="update-current">v1.0.0`, `class="update-latest"> → v1.1.0`,
		`/admin/devices/device/revoke`, `/admin/devices/retired/delete`,
		`datetime="2026-08-30T02:31:00Z"`, `data-local-datetime`, `data-local-date`, `data-local-clock`,
		"[hidden]{display:none!important}", "Codex Spark", "gpt-test-2", `data-sort="provider"`, `data-sort="model"`, `data-sort="state"`,
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
	for _, marker := range []string{"data-sort-table", "pill.hidden = false", "formatNumber", "renderUsageChart(range, report, mode, grouping)", "data-flash-dismiss", "prepareSeriesColors", "data-group-mode", "const visible = ordered.map", "moveRowAtY", "data-account-order-form", `time[data-local-datetime]`, `time[data-local-date]`, `time[data-local-clock]`} {
		if !strings.Contains(bundle, marker) {
			t.Fatalf("dashboard behavior bundle is missing %q", marker)
		}
	}
	if strings.Contains(bundle, ".toLocaleString(") {
		t.Fatal("dashboard behavior bundle still delegates numeric grouping to the browser locale")
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
