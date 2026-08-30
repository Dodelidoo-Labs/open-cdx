package helper

import (
	"bytes"
	"context"
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
)

func TestLocalTokenLifetimeAndSignature(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	token, err := IssueLocalToken("012345678901234567890123456789012345", now)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyLocalToken("012345678901234567890123456789012345", token, now.Add(4*time.Minute)) {
		t.Fatal("fresh local token was rejected")
	}
	if VerifyLocalToken("different-secret-012345678901234567890", token, now) || VerifyLocalToken("012345678901234567890123456789012345", token, now.Add(7*time.Minute)) {
		t.Fatal("tampered or expired local token was accepted")
	}
}

func TestCodexVersionNormalization(t *testing.T) {
	for input, expected := range map[string]string{
		"codex-cli 0.150.1": "0.150.1", "codex 1.2.3-beta.4": "1.2.3", "unexpected": "unknown",
	} {
		if actual := normalizeCodexVersion(input); actual != expected {
			t.Fatalf("normalizeCodexVersion(%q)=%q, expected %q", input, actual, expected)
		}
	}
}

func TestConfigSnippetUsesProductSpecificProviderID(t *testing.T) {
	snippet, err := ConfigSnippet(Config{ListenPort: DefaultPort, CatalogPath: "/tmp/catalog.json"}, "/tmp/router-helper")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`model_provider = "opencdx"`,
		`[model_providers.opencdx]`,
		`[model_providers.opencdx.auth]`,
	} {
		if !strings.Contains(snippet, expected) {
			t.Fatalf("config snippet does not contain %q:\n%s", expected, snippet)
		}
	}
	if strings.Contains(snippet, "model_providers.router") {
		t.Fatalf("config snippet still uses the generic legacy provider ID:\n%s", snippet)
	}
}

func TestRemoteClientClosesBodyWhenNoOutputIsRequested(t *testing.T) {
	body := &trackedReadCloser{Reader: bytes.NewBufferString(`{"status":"ok"}`)}
	client := &RemoteClient{
		BaseURL: "https://router.example.com", DeviceToken: "device",
		HTTP: &http.Client{Transport: helperRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: request}, nil
		})},
	}
	if _, err := client.JSON(context.Background(), http.MethodPost, "/test", nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if !body.closed {
		t.Fatal("successful response body was left open")
	}
}

func TestCatalogETagChangeRequiresCodexRestartAfterRouterRestart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("ETag", `"new-catalog"`)
		writer.Header().Set("X-OpenCDX-Codex-Restart-Required", "false")
		_, _ = writer.Write([]byte(`{"models":[{"slug":"new-model"}]}`))
	}))
	defer server.Close()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "helper.json")
	config := Config{
		RouterURL: server.URL, DeviceID: "device", DeviceName: "Mac", ListenPort: DefaultPort,
		CatalogPath: filepath.Join(directory, "catalog.json"), CatalogETag: `"old-catalog"`,
	}
	client, err := NewRemoteClient(config, "device-token")
	if err != nil {
		t.Fatal(err)
	}
	result, err := SyncCatalog(context.Background(), client, configPath, &config, "codex-cli test", false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.RestartRequired {
		t.Fatal("catalog change was hidden when the router lost its in-memory prior hash")
	}
}

func TestDaemonStatusIncludesSafeAccountAllowances(t *testing.T) {
	resetAt := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer device-token" {
			t.Fatalf("device token was not sent")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"route":{"connected":true,"state":"connected"},
			"accounts":[{
				"masked_email":"a***@example.com","plan":"plus","status":"ready",
				"paused":false,"primary":true,"quota_remaining":84,
				"quota_reset_at":"2030-01-02T03:04:05Z","reset_credits":2
			}]
		}`))
	}))
	defer server.Close()

	client := &RemoteClient{BaseURL: server.URL, DeviceToken: "device-token", HTTP: server.Client()}
	daemon := &Daemon{remote: client, status: LocalStatus{}}
	if err := daemon.refreshStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := daemon.currentStatus()
	if len(status.Accounts) != 1 {
		t.Fatalf("account allowances=%d, expected 1", len(status.Accounts))
	}
	account := status.Accounts[0]
	if account.MaskedEmail != "a***@example.com" || account.QuotaRemaining != 84 || !account.Primary || account.ResetCredits != 2 {
		t.Fatalf("unexpected account allowance: %#v", account)
	}
	if account.QuotaResetAt == nil || !account.QuotaResetAt.Equal(resetAt) {
		t.Fatalf("quota reset=%v, expected %v", account.QuotaResetAt, resetAt)
	}
}

func TestDaemonStatusNoticesCatalogWrittenBySiblingLogin(t *testing.T) {
	catalogPath := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(catalogPath, []byte(`{"models":[{"slug":"gpt-test"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	daemon := &Daemon{catalogPath: catalogPath, status: LocalStatus{CatalogSynced: false}}
	response := httptest.NewRecorder()
	daemon.controlStatus(response, httptest.NewRequest(http.MethodGet, "/control/status", nil))
	if response.Code != http.StatusOK || !daemon.currentStatus().CatalogSynced {
		t.Fatalf("catalog file was not reflected in status: code=%d status=%#v", response.Code, daemon.currentStatus())
	}
}

func TestDaemonTracksConcurrentInferenceActivity(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	daemon := &Daemon{}
	handler := daemon.trackInferenceActivity(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		started <- struct{}{}
		<-release
	}))

	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
		}()
	}
	<-started
	<-started
	if active := daemon.currentStatus().ActiveRequests; active != 2 {
		t.Fatalf("active requests=%d, expected 2", active)
	}
	close(release)
	wait.Wait()
	if active := daemon.currentStatus().ActiveRequests; active != 0 {
		t.Fatalf("active requests=%d after completion, expected 0", active)
	}
}

func TestDaemonProxyCancellationDoesNotOverwriteRouterHealth(t *testing.T) {
	daemon := &Daemon{status: LocalStatus{State: "connected", Connected: true}}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	response := httptest.NewRecorder()

	daemon.handleProxyError(response, request.WithContext(ctx), context.Canceled)

	status := daemon.currentStatus()
	if !status.Connected || status.State != "connected" || status.LastError != "" {
		t.Fatalf("client cancellation changed router health: %#v", status)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("client cancellation wrote a synthetic upstream error: %q", response.Body.String())
	}
}

func TestDaemonProxyUpstreamFailureMarksRouterUnreachable(t *testing.T) {
	daemon := &Daemon{status: LocalStatus{State: "connected", Connected: true}}
	response := httptest.NewRecorder()

	daemon.handleProxyError(response, httptest.NewRequest(http.MethodPost, "/v1/responses", nil), errors.New("connection refused"))

	status := daemon.currentStatus()
	if status.Connected || status.State != "error" || status.LastError != "remote router is unreachable" {
		t.Fatalf("upstream failure did not change router health: %#v", status)
	}
	if response.Code != http.StatusBadGateway {
		t.Fatalf("upstream failure status=%d, expected %d", response.Code, http.StatusBadGateway)
	}
}

func TestUnchangedCatalogDoesNotClearRestartReminder(t *testing.T) {
	daemon := &Daemon{status: LocalStatus{RestartRequired: true, LastError: "route failed"}}
	daemon.recordCatalogResult(CatalogResult{Changed: false})
	if !daemon.currentStatus().RestartRequired {
		t.Fatal("an unchanged catalog cleared the Codex restart reminder")
	}
	if daemon.currentStatus().LastError != "route failed" {
		t.Fatal("catalog synchronization cleared an unrelated route error")
	}
}

func TestCatalogRestartAcknowledgementClearsRemoteAndLocalStatus(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/catalog/restart-ack" {
			t.Fatalf("unexpected acknowledgement request: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer device-token" {
			t.Fatal("device token was not sent")
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	daemon := &Daemon{
		remote: &RemoteClient{BaseURL: server.URL, DeviceToken: "device-token", HTTP: server.Client()},
		status: LocalStatus{RestartRequired: true, LastError: "route failed"},
	}
	response := httptest.NewRecorder()
	daemon.controlCatalogRestartAck(response, httptest.NewRequest(http.MethodPost, "/control/catalog/restart-ack", nil))
	if response.Code != http.StatusOK || !called {
		t.Fatalf("acknowledgement failed: code=%d called=%v", response.Code, called)
	}
	if daemon.currentStatus().RestartRequired {
		t.Fatal("local restart reminder remained set after acknowledgement")
	}
	if daemon.currentStatus().LastError != "route failed" {
		t.Fatal("catalog acknowledgement cleared an unrelated route error")
	}
}

type helperRoundTripFunc func(*http.Request) (*http.Response, error)

func (function helperRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type trackedReadCloser struct {
	io.Reader
	closed bool
}

func (body *trackedReadCloser) Close() error {
	body.closed = true
	return nil
}

func TestAtomicWriteNeverExposesPartialCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	oldValue := bytes.Repeat([]byte("a"), 128*1024)
	newValue := bytes.Repeat([]byte("b"), 128*1024)
	if err := AtomicWrite(path, oldValue, 0o600); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	var invalid bool
	var mutex sync.Mutex
	wait.Add(1)
	go func() {
		defer wait.Done()
		for index := 0; index < 200; index++ {
			raw, err := os.ReadFile(path)
			if err == nil && !bytes.Equal(raw, oldValue) && !bytes.Equal(raw, newValue) {
				mutex.Lock()
				invalid = true
				mutex.Unlock()
			}
		}
	}()
	for index := 0; index < 20; index++ {
		value := oldValue
		if index%2 == 0 {
			value = newValue
		}
		if err := AtomicWrite(path, value, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	wait.Wait()
	if invalid {
		t.Fatal("reader observed a partially written catalog")
	}
}

func TestRemoteHTTPPolicy(t *testing.T) {
	base := Config{ListenPort: DefaultPort, CatalogPath: "/tmp/catalog.json"}
	base.RouterURL = "http://192.0.2.2:8080"
	if err := base.Validate(); err == nil {
		t.Fatal("plaintext LAN router was accepted")
	}
	base.InsecureDevelopment = true
	if err := base.Validate(); err != nil {
		t.Fatalf("explicit development override rejected: %v", err)
	}
	base.InsecureDevelopment = false
	base.RouterURL = "https://router.example.com"
	if err := base.Validate(); err != nil {
		t.Fatalf("HTTPS router rejected: %v", err)
	}
	base.RouterURL = "https://router.example.com/prefix"
	if err := base.Validate(); err == nil {
		t.Fatal("router URL with an unsupported path prefix was accepted")
	}
}
