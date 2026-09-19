package routing

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openrouter"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func cookieURL(raw string) *url.URL { u, _ := url.Parse(raw); return u }

func TestOpenAIRoutingCookieScopeAndLifetime(t *testing.T) {
	var cookies openAIRoutingCookies
	upstream := cookieURL("https://chatgpt.com/backend-api/codex/responses")
	jar := cookies.forAccount("account-a", upstream)
	jar.SetCookies(upstream, []*http.Cookie{
		{Name: "__oailb", Value: "route-a", Path: "/backend-api/codex", Secure: true, HttpOnly: true},
		{Name: "session", Value: "private-auth", Path: "/"},
		{Name: "__cf_bm", Value: "not-in-allowlist", Path: "/"},
	})
	got := jar.Cookies(upstream)
	if len(got) != 1 || got[0].Name != "__oailb" || got[0].Value != "route-a" {
		t.Fatalf("routing cookie missing or unapproved cookie stored: %v", got)
	}
	if len(jar.Cookies(cookieURL("https://chatgpt.com/backend-api/codex/responses/compact"))) != 1 {
		t.Fatal("cookie did not cover compaction")
	}
	for _, raw := range []string{
		"http://chatgpt.com/backend-api/codex/responses", "https://chatgpt.com/other",
		"https://foo.chatgpt.com/backend-api/codex/responses", "https://chatgpt.com:444/backend-api/codex/responses",
		"https://chatgpt.com.evil.example/backend-api/codex/responses",
	} {
		if len(jar.Cookies(cookieURL(raw))) != 0 {
			t.Fatalf("cookie escaped scope: %s", raw)
		}
	}
	if len(cookies.forAccount("account-b", upstream).Cookies(upstream)) != 0 {
		t.Fatal("account-a cookie leaked to account-b")
	}
	if cookies.forAccount("account-a", upstream) != jar {
		t.Fatal("same account lost routing state")
	}
	for _, raw := range []string{"http://chatgpt.com/", "https://evilchatgpt.com/", "https://api.openai.com/", "https://openrouter.ai/"} {
		if cookies.forAccount("account-a", cookieURL(raw)) != nil {
			t.Fatalf("unexpected eligible origin: %s", raw)
		}
	}
	if cookies.forAccount("", upstream) != nil {
		t.Fatal("cookie jar created without an account")
	}
	jar.SetCookies(upstream, []*http.Cookie{{Name: "__oailb", Value: "deleted", Path: "/backend-api/codex", MaxAge: -1}})
	if len(jar.Cookies(upstream)) != 0 {
		t.Fatal("Max-Age deletion ignored")
	}
	jar.SetCookies(upstream, []*http.Cookie{{Name: "__oailb", Value: "expired", Path: "/", Expires: time.Now().Add(-time.Hour)}})
	if len(jar.Cookies(upstream)) != 0 {
		t.Fatal("expired cookie retained")
	}
	jar.SetCookies(upstream, []*http.Cookie{{Name: "__oailb", Value: "wrong-domain", Domain: "other.example", Path: "/"}})
	if len(jar.Cookies(upstream)) != 0 {
		t.Fatal("unrelated cookie domain accepted")
	}
	jar.SetCookies(cookieURL("https://foo.chatgpt.com/"), []*http.Cookie{{Name: "__oailb", Value: "wrong-origin", Domain: "chatgpt.com", Path: "/"}})
	if len(jar.Cookies(upstream)) != 0 {
		t.Fatal("cookie accepted from a different origin")
	}
	jar.SetCookies(upstream, []*http.Cookie{{Name: "__oailb", Value: "parent-domain", Domain: ".chatgpt.com", Path: "/"}})
	if got = jar.Cookies(upstream); len(got) != 1 || got[0].Value != "parent-domain" {
		t.Fatal("valid domain cookie rejected")
	}
}

func TestOpenAIRoutingCookiesConcurrentAccounts(t *testing.T) {
	var cookies openAIRoutingCookies
	upstream := cookieURL("https://chatgpt.com/backend-api/codex/responses")
	var wg sync.WaitGroup
	for _, account := range []string{"a", "b", "c"} {
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(account string) {
				defer wg.Done()
				jar := cookies.forAccount(account, upstream)
				jar.SetCookies(upstream, []*http.Cookie{{Name: "__oailb", Value: account, Path: "/", Secure: true}})
				for _, c := range jar.Cookies(upstream) {
					if c.Value != account {
						t.Errorf("account %s received another account's routing cookie", account)
					}
				}
			}(account)
		}
	}
	wg.Wait()
}

func TestNativeProxyRetainsOnlyItsOwnRoutingCookie(t *testing.T) {
	var got []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = append(got, r.Header.Get("Cookie"))
		return &http.Response{StatusCode: 200, Header: http.Header{
			"Content-Type": {"application/json"}, "Set-Cookie": {"__oailb=upstream-route; Path=/backend-api/codex; Secure; HttpOnly", "session=private-auth; Path=/"},
		}, Body: io.NopCloser(strings.NewReader(`{"id":"ok"}`)), Request: r}, nil
	})
	// Even a caller-supplied global jar must not inject cookies into native requests.
	shared, _ := cookiejar.New(nil)
	shared.SetCookies(cookieURL("https://chatgpt.com/"), []*http.Cookie{{Name: "session", Value: "global-private", Path: "/"}})
	proxy, store, _ := proxyFixture(t, &http.Client{Transport: transport, Jar: shared}, "https://chatgpt.com/backend-api/codex", []routeFixture{{stable: "a", models: []string{"gpt-native"}}})
	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"gpt-native","input":[]}`))
		req.Header.Set("Cookie", "__oailb=client-injection; session=private-client")
		w := httptest.NewRecorder()
		proxy.ServeDeviceHTTP(w, req, DeviceContext{ID: "device"})
		if w.Code != 200 || w.Header().Get("Set-Cookie") != "" {
			t.Fatalf("response leaked cookies or failed: %d %v", w.Code, w.Header())
		}
	}
	if len(got) != 2 || got[0] != "" || got[1] != "__oailb=upstream-route" {
		t.Fatalf("unexpected cookie flow: %v", got)
	}
	logs, err := store.RequestLogs(context.Background(), storage.RequestLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(logs)
	if strings.Contains(string(encoded), "upstream-route") || strings.Contains(string(encoded), "private-auth") {
		t.Fatal("cookie value reached request logs")
	}
}

func TestNativeProxyQuotaFailoverIsolatesRoutingCookies(t *testing.T) {
	var got []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		account := r.Header.Get("ChatGPT-Account-ID")
		got = append(got, account+":"+r.Header.Get("Cookie"))
		status := 200
		if account == "a" {
			status = 429
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "Set-Cookie": {"__oailb=route-" + account + "; Path=/; Secure"}}, Body: io.NopCloser(strings.NewReader(`{"id":"ok"}`)), Request: r}, nil
	})
	proxy, _, accounts := proxyFixture(t, &http.Client{Transport: transport}, "https://chatgpt.com/backend-api/codex", []routeFixture{{stable: "a", models: []string{"gpt-native"}}, {stable: "b", models: []string{"gpt-native"}}})
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		proxy.ServeDeviceHTTP(w, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-native"}`)), DeviceContext{ID: "device"})
		if w.Code != 200 {
			t.Fatalf("failover failed: %d %s", w.Code, w.Body.String())
		}
	}
	if strings.Join(got, ",") != "a:,b:,b:__oailb=route-b" {
		t.Fatalf("failover mixed cookies: %v", got)
	}
	a := proxy.routingCookies.forAccount(accounts[0].ID, cookieURL("https://chatgpt.com/")).Cookies(cookieURL("https://chatgpt.com/"))
	if len(a) != 1 || a[0].Value != "route-a" {
		t.Fatal("429 cookie not retained in original account scope")
	}
}

func TestNativeCookieCannotEscapeRedirectOrProvider(t *testing.T) {
	for _, location := range []string{"https://chatgpt.com/elsewhere", "https://foo.chatgpt.com/", "https://evil.example/", "http://chatgpt.com/"} {
		t.Run(location, func(t *testing.T) {
			calls := 0
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 307, Header: http.Header{"Location": {location}, "Set-Cookie": {"__oailb=route; Path=/; Secure"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})
			proxy, _, _ := proxyFixture(t, &http.Client{Transport: transport}, "https://chatgpt.com/backend-api/codex", []routeFixture{{stable: "a", models: []string{"gpt-native"}}})
			w := httptest.NewRecorder()
			proxy.ServeDeviceHTTP(w, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-native"}`)), DeviceContext{ID: "device"})
			if calls != 1 || w.Code != 307 || w.Header().Get("Set-Cookie") != "" {
				t.Fatalf("redirect followed or cookie exposed: calls %d, status %d", calls, w.Code)
			}
		})
	}
	var sent []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sent = append(sent, r.Header.Get("Cookie"))
		return &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": {"__oailb=route; Path=/; Secure"}}, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
	})
	proxy := NewProxy(nil, nil, nil, nil, nil, &http.Client{Transport: transport}, false)
	native := openai.New(nil, "", "", "", "")
	thirdParty, _ := openrouter.New(nil, "https://chatgpt.com", "third-party-key")
	source := httptest.NewRequest("POST", "/v1/responses", nil)
	for _, target := range []routeTarget{
		{provider: "openai", account: storage.Account{ID: "a"}, executor: native, credential: providers.Credential{AccountID: "a"}, url: "https://chatgpt.com/responses"},
		{provider: "openrouter", executor: thirdParty, url: "https://chatgpt.com/responses"},
		{provider: "openai", account: storage.Account{ID: "b"}, executor: native, credential: providers.Credential{AccountID: "b"}, url: "https://chatgpt.com/responses"},
		{provider: "openai", account: storage.Account{ID: "a"}, executor: native, credential: providers.Credential{AccountID: "a"}, url: "https://chatgpt.com/responses"},
	} {
		response, err := proxy.attempt(context.Background(), source, target, []byte("{}"))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	if strings.Join(sent, ",") != ",,,__oailb=route" {
		t.Fatalf("cookie crossed provider/account scope: %v", sent)
	}
}

func TestNativeProxyAuthRefreshKeepsRoutingCookie(t *testing.T) {
	var got []string
	refreshCalls := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status, body := 200, `{"id":"ok"}`
		headers := http.Header{"Content-Type": {"application/json"}}
		if r.URL.Path == "/oauth/token" {
			refreshCalls++
			if r.Header.Get("Cookie") != "" {
				t.Fatal("inference routing cookie leaked into OAuth refresh")
			}
			body = `{"access_token":"refreshed-access","expires_in":3600}`
		} else {
			got = append(got, r.Header.Get("Cookie"))
			if r.Header.Get("Authorization") != "Bearer refreshed-access" {
				status = 401
				headers.Add("Set-Cookie", "__oailb=before-refresh; Path=/; Secure")
			}
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	proxy, _, _ := proxyFixture(t, &http.Client{Transport: transport}, "https://chatgpt.com/backend-api/codex", []routeFixture{{stable: "a", models: []string{"gpt-native"}}})
	w := httptest.NewRecorder()
	proxy.ServeDeviceHTTP(w, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-native"}`)), DeviceContext{ID: "device"})
	if w.Code != 200 || refreshCalls != 1 || strings.Join(got, ",") != ",__oailb=before-refresh" {
		t.Fatalf("refresh lost cookie: status %d, refreshes %d, cookies %v", w.Code, refreshCalls, got)
	}
}
