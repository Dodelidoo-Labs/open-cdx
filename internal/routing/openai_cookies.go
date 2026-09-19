package routing

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
)

// Routing cookies belong to the upstream connection, not to the local client.
// Keep them in memory, isolated by selected account and exact upstream origin.
// In particular, never attach a shared cookie jar to the multi-provider client.
type openAIRoutingCookies struct {
	mu   sync.Mutex
	jars map[openAICookieScope]*openAIRoutingJar
}

type openAICookieScope struct {
	accountID string
	origin    string
}

func (cookies *openAIRoutingCookies) forAccount(accountID string, upstream *url.URL) http.CookieJar {
	if accountID == "" || !chatGPTCookieURL(upstream) {
		return nil
	}
	scope := openAICookieScope{accountID: accountID, origin: cookieOrigin(upstream)}
	cookies.mu.Lock()
	defer cookies.mu.Unlock()
	if cookies.jars == nil {
		cookies.jars = make(map[openAICookieScope]*openAIRoutingJar)
	}
	if jar := cookies.jars[scope]; jar != nil {
		return jar
	}
	jar, _ := cookiejar.New(nil) // No options can fail; the wrapper enforces exact-origin isolation.
	isolated := &openAIRoutingJar{origin: scope.origin, jar: jar}
	cookies.jars[scope] = isolated
	return isolated
}

type openAIRoutingJar struct {
	origin string
	jar    *cookiejar.Jar // Handles cookie domain, path, Secure, expiry, and concurrent access.
}

func (jar *openAIRoutingJar) SetCookies(upstream *url.URL, cookies []*http.Cookie) {
	if !chatGPTCookieURL(upstream) || cookieOrigin(upstream) != jar.origin {
		return
	}
	var allowed []*http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == "__oailb" {
			allowed = append(allowed, cookie)
		}
	}
	jar.jar.SetCookies(upstream, allowed)
}

func (jar *openAIRoutingJar) Cookies(upstream *url.URL) []*http.Cookie {
	if !chatGPTCookieURL(upstream) || cookieOrigin(upstream) != jar.origin {
		return nil
	}
	return jar.jar.Cookies(upstream)
}

func cookieOrigin(upstream *url.URL) string {
	return upstream.Scheme + "://" + strings.ToLower(upstream.Host)
}

func chatGPTCookieURL(upstream *url.URL) bool {
	if upstream == nil || upstream.Scheme != "https" {
		return false
	}
	// Same first-party hosts as official Codex's chatgpt_hosts.rs. Merely
	// containing "chatgpt.com" must not opt an arbitrary endpoint into this jar.
	host := strings.ToLower(upstream.Hostname())
	return host == "chatgpt.com" || strings.HasSuffix(host, ".chatgpt.com") ||
		host == "chat.openai.com" || host == "chatgpt-staging.com" ||
		strings.HasSuffix(host, ".chatgpt-staging.com")
}
