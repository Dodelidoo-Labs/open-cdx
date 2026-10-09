package routing

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeCodexAccount is one account as the fake OpenAI backend sees it.
type fakeCodexAccount struct {
	allowance bool // ordinary allowance left
	credits   bool // purchased credits left
}

// fakeCodexBackend answers usage polls and Responses requests the way the
// ChatGPT backend does: allowance first, then credits, then a 429.
type fakeCodexBackend struct {
	mutex    sync.Mutex
	accounts map[string]*fakeCodexAccount
	served   []string
	consumed int
}

func (backend *fakeCodexBackend) set(stable string, allowance, credits bool) {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	backend.accounts[stable] = &fakeCodexAccount{allowance: allowance, credits: credits}
}

func (backend *fakeCodexBackend) RoundTrip(request *http.Request) (*http.Response, error) {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	stable := request.Header.Get("ChatGPT-Account-ID")
	account := backend.accounts[stable]
	reply := func(status int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}
	switch {
	case strings.HasSuffix(request.URL.Path, "/consume"):
		backend.consumed++
		return reply(http.StatusOK, `{"code":"reset"}`)
	case strings.HasSuffix(request.URL.Path, "/wham/rate-limit-reset-credits"):
		return reply(http.StatusOK, `{"available_count":2}`)
	case strings.HasSuffix(request.URL.Path, "/wham/usage"):
		used, allowed, reached := 40, true, `null`
		if !account.allowance {
			used, allowed, reached = 100, false, `{"type":"rate_limit_reached"}`
		}
		return reply(http.StatusOK, fmt.Sprintf(`{"plan_type":"plus","rate_limit":{"allowed":%t,"limit_reached":%t,"primary_window":{"used_percent":%d,"limit_window_seconds":604800,"reset_after_seconds":86400}},"rate_limit_reached_type":%s,"credits":{"has_credits":%t,"unlimited":false,"balance":"%d"},"rate_limit_reset_credits":{"available_count":2}}`,
			allowed, !allowed, used, reached, account.credits, map[bool]int{true: 120, false: 0}[account.credits]))
	case strings.HasSuffix(request.URL.Path, "/responses"):
		if !account.allowance && !account.credits {
			return reply(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached"}}`)
		}
		backend.served = append(backend.served, stable)
		return reply(http.StatusOK, `{"id":"ok"}`)
	}
	return reply(http.StatusNotFound, `{}`)
}

func (backend *fakeCodexBackend) lastServed() string {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	if len(backend.served) == 0 {
		return ""
	}
	return backend.served[len(backend.served)-1]
}

// TestCodexKeepsRunningAsAllowanceAndCreditsRunOut walks one session through
// every depletion stage without waiting for a real allowance to run out.
func TestCodexKeepsRunningAsAllowanceAndCreditsRunOut(t *testing.T) {
	backend := &fakeCodexBackend{accounts: map[string]*fakeCodexAccount{}}
	backend.set("primary", true, true)
	backend.set("fallback", true, false)
	backend.set("empty", false, false)
	proxy, _, _ := proxyFixture(t, &http.Client{Transport: backend}, "https://upstream.invalid", []routeFixture{
		{stable: "primary", quota: 0, models: []string{"gpt-shared"}},
		{stable: "fallback", quota: 0, models: []string{"gpt-shared"}},
		{stable: "empty", quota: 0, models: []string{"gpt-shared"}},
	})
	ctx := context.Background()
	poll := func() {
		t.Helper()
		if err := proxy.accounts.RefreshQuotas(ctx); err != nil {
			t.Fatal(err)
		}
	}
	send := func() *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"gpt-shared","input":[]}`))
		writer := httptest.NewRecorder()
		proxy.ServeDeviceHTTP(writer, request, DeviceContext{ID: "device"})
		return writer
	}
	expect := func(stage, account string) {
		t.Helper()
		writer := send()
		if writer.Code != http.StatusOK || backend.lastServed() != account {
			t.Fatalf("%s: status %d, served by %q, want %q: %s", stage, writer.Code, backend.lastServed(), account, writer.Body.String())
		}
	}

	poll()
	expect("all allowances", "primary")

	// Primary's allowance runs out. Upstream keeps serving it from credits,
	// but the router prefers the fallback's free allowance once polled.
	backend.set("primary", false, true)
	expect("before poll, upstream spends primary credits", "primary")
	poll()
	expect("after poll, fallback allowance first", "fallback")

	// Fallback runs out with no credits; the 429 fails over to primary's
	// credits within the same request.
	backend.set("fallback", false, false)
	expect("fallback rejected, primary credits", "primary")
	poll()
	expect("after poll, primary credits", "primary")

	// Primary's credits run out too: nothing is left, and the client is told.
	backend.set("primary", false, false)
	if writer := send(); writer.Code != http.StatusTooManyRequests || !strings.Contains(writer.Body.String(), "quota_exhausted") {
		t.Fatalf("all exhausted: status %d: %s", writer.Code, writer.Body.String())
	}

	// A reset of the weekly window brings the fallback back.
	backend.set("fallback", true, false)
	poll()
	expect("fallback reset", "fallback")

	if backend.consumed != 0 {
		t.Fatalf("router redeemed %d reset tickets on its own", backend.consumed)
	}
}

func TestQuotaFailoverTriesEveryAccount(t *testing.T) {
	backend := &fakeCodexBackend{accounts: map[string]*fakeCodexAccount{}}
	fixtures := []routeFixture{}
	for _, stable := range []string{"one", "two", "three", "four"} {
		backend.set(stable, stable == "four", false)
		fixtures = append(fixtures, routeFixture{stable: stable, quota: 0, models: []string{"gpt-shared"}})
	}
	proxy, _, _ := proxyFixture(t, &http.Client{Transport: backend}, "https://upstream.invalid", fixtures)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"gpt-shared","input":[]}`))
	writer := httptest.NewRecorder()
	proxy.ServeDeviceHTTP(writer, request, DeviceContext{ID: "device"})
	if writer.Code != http.StatusOK || backend.lastServed() != "four" {
		t.Fatalf("status %d, served by %q: %s", writer.Code, backend.lastServed(), writer.Body.String())
	}
}
