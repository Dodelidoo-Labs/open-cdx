package helper

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"time"
)

type OAuthResult struct {
	MaskedEmail string `json:"masked_email"`
	Plan        string `json:"plan"`
	Replaced    bool   `json:"replaced"`
}

func RunOpenAILogin(ctx context.Context, client *RemoteClient, codexVersion string, replace bool) (OAuthResult, error) {
	listener, port, err := callbackListener()
	if err != nil {
		return OAuthResult{}, err
	}
	defer listener.Close()
	redirectURI := fmt.Sprintf("http://localhost:%d/auth/callback", port)
	var start OAuthStartResponse
	if _, err = client.JSON(ctx, http.MethodPost, "/api/v1/oauth/openai/start", map[string]string{"redirect_uri": redirectURI}, &start, true); err != nil {
		return OAuthResult{}, err
	}
	authorizationURL, err := url.Parse(start.AuthorizationURL)
	if err != nil {
		return OAuthResult{}, errors.New("router returned an invalid authorization URL")
	}
	expectedState := authorizationURL.Query().Get("state")
	if expectedState == "" {
		return OAuthResult{}, errors.New("router authorization URL omitted OAuth state")
	}
	resultChannel := make(chan oauthCallbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/callback", func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if query.Get("state") != expectedState {
			oauthPage(writer, false, "The OAuth state did not match. Close this page and try again.")
			select {
			case resultChannel <- oauthCallbackResult{err: errors.New("OAuth state mismatch")}:
			default:
			}
			return
		}
		if oauthError := query.Get("error"); oauthError != "" {
			oauthPage(writer, false, "OpenAI login was cancelled or denied. Close this page and try again.")
			select {
			case resultChannel <- oauthCallbackResult{err: errors.New("OpenAI authorization was denied")}:
			default:
			}
			return
		}
		code := query.Get("code")
		if code == "" || len(code) > 8192 {
			oauthPage(writer, false, "The callback did not contain a valid authorization code.")
			select {
			case resultChannel <- oauthCallbackResult{err: errors.New("OAuth callback omitted its code")}:
			default:
			}
			return
		}
		var complete struct {
			Account struct {
				MaskedEmail string `json:"masked_email"`
				Plan        string `json:"plan"`
			} `json:"account"`
			Replaced bool `json:"replaced"`
		}
		_, completeErr := client.JSON(request.Context(), http.MethodPost, "/api/v1/oauth/openai/complete", map[string]any{
			"transaction_id": start.TransactionID, "state": expectedState, "code": code,
			"client_version": codexVersion, "replace": replace,
		}, &complete, true)
		if completeErr != nil {
			oauthPage(writer, false, "The router could not validate this account. Return to the menu app for details.")
			select {
			case resultChannel <- oauthCallbackResult{err: completeErr}:
			default:
			}
			return
		}
		oauthPage(writer, true, "Account connected. You can close this browser tab.")
		select {
		case resultChannel <- oauthCallbackResult{result: OAuthResult{MaskedEmail: complete.Account.MaskedEmail, Plan: complete.Account.Plan, Replaced: complete.Replaced}}:
		default:
		}
	})
	callbackServer := &http.Server{
		Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 15 * time.Second,
		IdleTimeout: 15 * time.Second, MaxHeaderBytes: 32 << 10,
	}
	serverError := make(chan error, 1)
	go func() { serverError <- callbackServer.Serve(listener) }()
	if err = OpenURL(start.AuthorizationURL); err != nil {
		_ = callbackServer.Close()
		return OAuthResult{}, err
	}
	deadline := time.NewTimer(time.Until(start.ExpiresAt) + 15*time.Second)
	defer deadline.Stop()
	select {
	case <-ctx.Done():
		_ = callbackServer.Close()
		return OAuthResult{}, ctx.Err()
	case <-deadline.C:
		_ = callbackServer.Close()
		return OAuthResult{}, errors.New("OpenAI login expired before the callback completed")
	case err = <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			return OAuthResult{}, errors.New("OAuth callback listener stopped unexpectedly")
		}
		return OAuthResult{}, err
	case callback := <-resultChannel:
		shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = callbackServer.Shutdown(shutdownContext)
		return callback.result, callback.err
	}
}

type oauthCallbackResult struct {
	result OAuthResult
	err    error
}

func callbackListener() (net.Listener, int, error) {
	for _, port := range []int{1455, 1457} {
		listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
		if err == nil {
			return listener, port, nil
		}
	}
	return nil, 0, errors.New("OAuth callback ports 1455 and 1457 are both unavailable")
}

func oauthPage(writer http.ResponseWriter, success bool, message string) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	color := "#ff8795"
	title := "Login failed"
	if success {
		color, title = "#65d1a5", "Account connected"
	}
	_, _ = fmt.Fprintf(writer, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>%s</title><style>body{font:16px system-ui;background:#0e1116;color:#edf2f7;display:grid;place-items:center;min-height:100vh;margin:0}.card{max-width:440px;padding:30px;border:1px solid #2b3442;border-radius:18px;background:#171c24}h1{color:%s}</style><main class="card"><h1>%s</h1><p>%s</p></main>`, html.EscapeString(title), color, html.EscapeString(title), html.EscapeString(message))
}

func OpenURL(rawURL string) error {
	return openURL(rawURL, false)
}

func OpenRouterURL(rawURL string, allowInsecureDevelopment bool) error {
	return openURL(rawURL, allowInsecureDevelopment)
}

func openURL(rawURL string, allowInsecureDevelopment bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("refusing to open an invalid URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != URLScheme && !(parsed.Scheme == "http" && (isLoopback(parsed.Hostname()) || allowInsecureDevelopment)) {
		return errors.New("refusing to open an unsupported URL")
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("/usr/bin/open", rawURL)
	case "linux":
		command = exec.Command("xdg-open", rawURL)
	default:
		return errors.New("opening a browser is not supported on this platform")
	}
	if err := command.Start(); err != nil {
		return errors.New("could not open the default browser")
	}
	return nil
}
