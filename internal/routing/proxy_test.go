package routing

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/opencdx/opencdx/internal/accounts"
	"github.com/opencdx/opencdx/internal/catalog"
	secure "github.com/opencdx/opencdx/internal/crypto"
	"github.com/opencdx/opencdx/internal/providers/openai"
	"github.com/opencdx/opencdx/internal/storage"
)

func TestNativeProxyPreservesBodyAndMetadataWhileReplacingAuthentication(t *testing.T) {
	requestBody := []byte("{ \"model\" : \"gpt-native\", \"input\" : [ { \"role\":\"user\", \"content\":\"private\" } ] }")
	var receivedBody []byte
	var receivedHeaders http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedBody, _ = io.ReadAll(request.Body)
		receivedHeaders = request.Header.Clone()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"response","usage":{"input_tokens":4,"output_tokens":2}}`))
	}))
	defer upstream.Close()
	proxy, _, _ := proxyFixture(t, upstream.Client(), upstream.URL, []routeFixture{{stable: "stable-native", quota: 10, models: []string{"gpt-native"}}})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))
	request.Header.Set("Authorization", "Bearer local-short-lived")
	request.Header.Set("Cookie", "browser=secret")
	request.Header.Set("ChatGPT-Account-ID", "attacker-controlled")
	requiredMetadata := map[string]string{
		"X-Codex-Test-Metadata": "preserve-me", "X-Codex-Turn-Metadata": "turn-metadata",
		"X-Oai-Attestation": "attestation", "Originator": "codex_cli_rs", "Version": "codex-cli 9.9",
		"User-Agent": "codex-cli/test", "Thread-Id": "thread-sticky", "Session-Id": "session",
		"OpenAI-Beta": "responses=experimental", "X-OpenAI-Subagent": "review",
		"X-OpenAI-Memgen-Request": "true", "X-OpenAI-Internal-Codex-Responses-Lite": "true",
		"X-ResponsesAPI-Feature": "daybreak",
	}
	for name, value := range requiredMetadata {
		request.Header.Set(name, value)
	}
	writer := httptest.NewRecorder()
	proxy.ServeDeviceHTTP(writer, request, DeviceContext{ID: "device"})
	if writer.Code != http.StatusOK {
		t.Fatalf("proxy returned %d: %s", writer.Code, writer.Body.String())
	}
	if !bytes.Equal(receivedBody, requestBody) {
		t.Fatalf("native body changed\nwant %s\n got %s", requestBody, receivedBody)
	}
	if receivedHeaders.Get("Authorization") != "Bearer access-stable-native" || receivedHeaders.Get("ChatGPT-Account-ID") != "stable-native" {
		t.Fatal("router authentication was not installed")
	}
	if receivedHeaders.Get("Cookie") != "" {
		t.Fatal("security-sensitive cookie was forwarded")
	}
	for name, value := range requiredMetadata {
		if receivedHeaders.Get(name) != value {
			t.Fatalf("required Codex metadata header %s changed: %q", name, receivedHeaders.Get(name))
		}
	}
}

func TestQuotaResponseSwitchesBeforeAnyClientBytes(t *testing.T) {
	var mutex sync.Mutex
	var attempts []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		account := request.Header.Get("ChatGPT-Account-ID")
		mutex.Lock()
		attempts = append(attempts, account)
		mutex.Unlock()
		status, body := http.StatusTooManyRequests, "quota"
		if account == "second-stable" {
			status, body = http.StatusOK, `{"id":"ok"}`
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(body)), Request: request}, nil
	})
	proxy, _, _ := proxyFixture(t, &http.Client{Transport: transport}, "https://upstream.invalid", []routeFixture{
		{stable: "first-stable", quota: 1, models: []string{"gpt-shared"}},
		{stable: "second-stable", quota: 50, models: []string{"gpt-shared"}},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"gpt-shared","input":[]}`))
	writer := httptest.NewRecorder()
	proxy.ServeDeviceHTTP(writer, request, DeviceContext{ID: "device"})
	if writer.Code != http.StatusOK || writer.Body.String() != `{"id":"ok"}` {
		t.Fatalf("switch failed: %d %s", writer.Code, writer.Body.String())
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(attempts) != 2 || attempts[0] != "first-stable" || attempts[1] != "second-stable" {
		t.Fatalf("unexpected account attempts: %v", attempts)
	}
}

func TestNoRetryAfterPartialStreaming(t *testing.T) {
	var attempts int
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(&oneChunkThenError{chunk: []byte("data: partial\n\n")}), Request: request}, nil
	})
	proxy, _, _ := proxyFixture(t, &http.Client{Transport: transport}, "https://upstream.invalid", []routeFixture{{stable: "stream-account", quota: 1, models: []string{"gpt-stream"}}})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"gpt-stream","stream":true}`))
	writer := httptest.NewRecorder()
	proxy.ServeDeviceHTTP(writer, request, DeviceContext{ID: "device"})
	if attempts != 1 {
		t.Fatalf("partially streamed request was replayed %d times", attempts)
	}
	if writer.Body.String() != "data: partial\n\n" {
		t.Fatalf("partial bytes were not forwarded: %q", writer.Body.String())
	}
}

func TestRoutingErrorsRedactSecrets(t *testing.T) {
	message := publicRouteError(errors.New("Bearer token credential leaked"))
	if message != "provider authentication or routing failed" {
		t.Fatalf("secret-bearing error was not redacted: %q", message)
	}
}

type routeFixture struct {
	stable string
	quota  float64
	models []string
}

func proxyFixture(t *testing.T, httpClient *http.Client, responsesBase string, fixtures []routeFixture) (*Proxy, *storage.Store, []*storage.Account) {
	t.Helper()
	box, _ := secure.NewBox(bytes.Repeat([]byte{6}, 32))
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	accountsCreated := make([]*storage.Account, 0, len(fixtures))
	for _, fixture := range fixtures {
		account, _, createErr := store.PutAccount(context.Background(), storage.AccountInput{
			Credential:  storage.OpenAICredential{AccountID: fixture.stable, AccessToken: "access-" + fixture.stable, RefreshToken: "refresh-" + fixture.stable, IDToken: "id-" + fixture.stable, ExpiresAt: time.Now().Add(time.Hour)},
			MaskedEmail: fixture.stable + "@masked", Plan: "plus", Status: "ready", QuotaUsedPercent: fixture.quota,
			EntitledModels: fixture.models, RawCatalogSnapshot: []byte(`{"models":[]}`),
		}, false)
		if createErr != nil {
			t.Fatal(createErr)
		}
		copy := account
		accountsCreated = append(accountsCreated, &copy)
	}
	openAIClient := openai.New(httpClient, "https://auth.invalid", "client", responsesBase, "https://chatgpt.invalid")
	accountManager := accounts.NewManager(store, openAIClient)
	catalogManager := catalog.NewManager(store)
	selector := NewSelector(store, bytes.Repeat([]byte{2}, 32))
	proxy := NewProxy(store, accountManager, catalogManager, selector, NewStatusRegistry(), httpClient, true)
	return proxy, store, accountsCreated
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type oneChunkThenError struct {
	chunk []byte
	sent  bool
}

func (reader *oneChunkThenError) Read(destination []byte) (int, error) {
	if reader.sent {
		return 0, errors.New("simulated upstream disconnect")
	}
	reader.sent = true
	return copy(destination, reader.chunk), nil
}
