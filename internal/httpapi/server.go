package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/accounts"
	"github.com/Dodelidoo-Labs/open-cdx/internal/catalog"
	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/ollama"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openrouter"
	"github.com/Dodelidoo-Labs/open-cdx/internal/routing"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
	"github.com/Dodelidoo-Labs/open-cdx/internal/telemetry"
	"github.com/Dodelidoo-Labs/open-cdx/internal/usagehistory"
	appversion "github.com/Dodelidoo-Labs/open-cdx/internal/version"
	site "github.com/Dodelidoo-Labs/open-cdx/web"
)

const sessionLifetime = 12 * time.Hour

type Server struct {
	store       *storage.Store
	accounts    *accounts.Manager
	catalog     *catalog.Manager
	proxy       *routing.Proxy
	status      *routing.StatusRegistry
	adminSecret string
	publicURL   *url.URL
	insecureDev bool
	httpClient  *http.Client
	updates     *appversion.UpdateChecker
	templates   *template.Template
	sessionsMu  sync.Mutex
	sessions    map[string]adminSession
	handler     http.Handler
}

type adminSession struct {
	CSRF      string
	ExpiresAt time.Time
}

type deviceContextKey struct{}

func New(store *storage.Store, accountManager *accounts.Manager, catalogManager *catalog.Manager, proxy *routing.Proxy, status *routing.StatusRegistry, adminSecret, publicBaseURL string, insecureDev bool, httpClient *http.Client) (*Server, error) {
	parsedURL, err := url.Parse(publicBaseURL)
	if err != nil {
		return nil, err
	}
	templates, err := template.New("site").Funcs(template.FuncMap{"number": formatInteger}).ParseFS(site.Templates, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse dashboard templates: %w", err)
	}
	updates := appversion.NewUpdateChecker(httpClient)
	server := &Server{
		store: store, accounts: accountManager, catalog: catalogManager, proxy: proxy, status: status,
		adminSecret: adminSecret, publicURL: parsedURL, insecureDev: insecureDev, httpClient: httpClient,
		updates: updates, templates: templates, sessions: make(map[string]adminSession),
	}
	// Prime the asynchronous cache at startup. GitHub availability never gates
	// router startup or dashboard rendering.
	_ = updates.Snapshot()
	server.handler = server.routes()
	return server, nil
}

func formatInteger(value int) string {
	raw := strconv.Itoa(value)
	sign := ""
	if strings.HasPrefix(raw, "-") {
		sign = "-"
		raw = strings.TrimPrefix(raw, "-")
	}
	if len(raw) <= 3 {
		return sign + raw
	}
	var formatted strings.Builder
	formatted.Grow(len(raw) + len(raw)/3)
	formatted.WriteString(sign)
	for index, digit := range raw {
		if index > 0 && (len(raw)-index)%3 == 0 {
			formatted.WriteByte('\'')
		}
		formatted.WriteRune(digit)
	}
	return formatted.String()
}

func assetVersion() string {
	return assetVersionFor(appversion.Display(), appversion.Commit)
}

func assetVersionFor(version, commit string) string {
	commit = strings.TrimSpace(commit)
	if commit == "" || commit == "unknown" {
		return version
	}
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return version + "-" + commit
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	server.securityHeaders(writer, request)
	server.handler.ServeHTTP(writer, request)
}

func (server *Server) routes() http.Handler {
	mux := http.NewServeMux()
	staticAsset := func(name, contentType string) http.HandlerFunc {
		return func(writer http.ResponseWriter, request *http.Request) {
			asset, err := site.Static.ReadFile("static/" + name)
			if err != nil {
				http.NotFound(writer, request)
				return
			}
			writer.Header().Set("Content-Type", contentType)
			_, _ = writer.Write(asset)
		}
	}
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /readyz", server.ready)
	mux.HandleFunc("POST /api/v1/enroll", server.enroll)
	mux.HandleFunc("POST /api/v1/enroll/status", server.enrollmentStatus)
	mux.HandleFunc("POST /api/v1/enroll/ack", server.enrollmentAck)
	mux.Handle("GET /api/v1/device/status", server.device(http.HandlerFunc(server.deviceStatus)))
	mux.Handle("POST /api/v1/oauth/openai/start", server.device(http.HandlerFunc(server.oauthStart)))
	mux.Handle("POST /api/v1/oauth/openai/complete", server.device(http.HandlerFunc(server.oauthComplete)))
	mux.Handle("GET /api/v1/catalog", server.device(http.HandlerFunc(server.getCatalog)))
	mux.Handle("POST /api/v1/catalog/refresh", server.device(http.HandlerFunc(server.refreshCatalog)))
	mux.Handle("POST /api/v1/catalog/restart-ack", server.device(http.HandlerFunc(server.acknowledgeCatalogRestart)))
	mux.Handle("POST /api/v1/quotas/refresh", server.device(http.HandlerFunc(server.refreshQuotas)))
	mux.Handle("POST /api/v1/telemetry/reconcile", server.device(http.HandlerFunc(server.reconcileUsage)))
	mux.Handle("POST /api/v1/telemetry/reset", server.device(http.HandlerFunc(server.resetTelemetry)))
	mux.Handle("POST /v1/responses", server.device(http.HandlerFunc(server.responses)))
	mux.Handle("POST /v1/responses/compact", server.device(http.HandlerFunc(server.responses)))
	mux.HandleFunc("GET /admin/login", server.loginPage)
	mux.HandleFunc("GET /assets/dashboard.js", staticAsset("dashboard.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /assets/dashboard.css", staticAsset("dashboard.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /assets/material-symbols-outlined.woff2", staticAsset("material-symbols-outlined.woff2", "font/woff2"))
	mux.HandleFunc("GET /assets/opencdx-router-logo.png", staticAsset("opencdx-router-logo.png", "image/png"))
	mux.HandleFunc("GET /assets/favicon-32x32.png", staticAsset("favicon-32x32.png", "image/png"))
	mux.HandleFunc("GET /assets/favicon-16x16.png", staticAsset("favicon-16x16.png", "image/png"))
	mux.HandleFunc("GET /assets/apple-touch-icon.png", staticAsset("apple-touch-icon.png", "image/png"))
	mux.HandleFunc("POST /admin/login", server.login)
	mux.Handle("POST /admin/logout", server.admin(http.HandlerFunc(server.logout)))
	mux.Handle("GET /admin", server.admin(http.HandlerFunc(server.dashboard)))
	mux.Handle("GET /admin/telemetry", server.admin(http.HandlerFunc(server.adminTelemetry)))
	mux.Handle("POST /admin/telemetry/reset", server.admin(http.HandlerFunc(server.adminResetTelemetry)))
	mux.Handle("POST /admin/refresh", server.admin(http.HandlerFunc(server.adminRefresh)))
	mux.Handle("POST /admin/accounts/reorder", server.admin(http.HandlerFunc(server.adminAccountOrder)))
	mux.Handle("POST /admin/accounts/{id}/{action}", server.admin(http.HandlerFunc(server.adminAccount)))
	mux.Handle("POST /admin/devices/{id}/{action}", server.admin(http.HandlerFunc(server.adminDevice)))
	mux.Handle("POST /admin/providers/{name}", server.admin(http.HandlerFunc(server.adminProvider)))
	mux.Handle("POST /admin/providers/{name}/remove-secret", server.admin(http.HandlerFunc(server.adminProviderRemoveSecret)))
	mux.HandleFunc("GET /", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}
		http.Redirect(writer, request, "/admin", http.StatusSeeOther)
	})
	return mux
}

func (server *Server) securityHeaders(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("X-Frame-Options", "DENY")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	writer.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	if server.publicURL.Scheme == "https" {
		writer.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}
}

func (server *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"status": "ok"})
}

func (server *Server) ready(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	if err := server.store.Database().PingContext(ctx); err != nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]any{"status": "not_ready"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"status": "ready"})
}

func (server *Server) enroll(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	enrollment, err := server.store.CreateEnrollment(request.Context(), input.Name)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_device", err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, enrollment)
}

func (server *Server) enrollmentStatus(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		DeviceID string `json:"device_id"`
		Secret   string `json:"enrollment_secret"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	enrollment, err := server.store.EnrollmentStatus(request.Context(), input.DeviceID, input.Secret)
	switch {
	case err == nil:
		writeJSON(writer, http.StatusOK, enrollment)
	case errors.Is(err, storage.ErrEnrollmentPending):
		writeJSON(writer, http.StatusAccepted, enrollment)
	case errors.Is(err, storage.ErrEnrollmentRejected):
		writeAPIError(writer, http.StatusForbidden, "enrollment_rejected", "device enrollment was rejected or revoked")
	case errors.Is(err, storage.ErrEnrollmentComplete):
		writeAPIError(writer, http.StatusGone, "enrollment_complete", "the one-time device credential was already acknowledged")
	default:
		writeAPIError(writer, http.StatusNotFound, "enrollment_not_found", "device enrollment was not found")
	}
}

func (server *Server) enrollmentAck(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		DeviceID string `json:"device_id"`
		Secret   string `json:"enrollment_secret"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	if err := server.store.AcknowledgeEnrollment(request.Context(), input.DeviceID, input.Secret); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "acknowledgement_failed", "device enrollment could not be acknowledged")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) device(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		token := bearerToken(request.Header.Get("Authorization"))
		device, err := server.store.AuthenticateDevice(request.Context(), token)
		if err != nil {
			writer.Header().Set("WWW-Authenticate", "Bearer")
			writeAPIError(writer, http.StatusUnauthorized, "invalid_device", "device credential is invalid or revoked")
			return
		}
		request.Header.Del("Authorization")
		ctx := context.WithValue(request.Context(), deviceContextKey{}, device)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (server *Server) deviceStatus(writer http.ResponseWriter, request *http.Request) {
	device := currentDevice(request.Context())
	status := server.status.Get(device.ID)
	accounts, _ := server.safeAccounts(request.Context())
	providers, _ := server.store.Providers(request.Context())
	writeJSON(writer, http.StatusOK, map[string]any{"device": device, "route": status, "accounts": accounts, "providers": providers})
}

func (server *Server) oauthStart(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		RedirectURI string `json:"redirect_uri"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	device := currentDevice(request.Context())
	start, err := server.accounts.StartOAuth(request.Context(), device.ID, input.RedirectURI)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "oauth_start_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, start)
}

func (server *Server) oauthComplete(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		TransactionID string `json:"transaction_id"`
		State         string `json:"state"`
		Code          string `json:"code"`
		ClientVersion string `json:"client_version"`
		Replace       bool   `json:"replace"`
	}
	if !decodeJSON(writer, request, &input) {
		return
	}
	device := currentDevice(request.Context())
	result, err := server.accounts.CompleteOAuth(request.Context(), device.ID, input.TransactionID, input.State, input.Code, normalizedVersion(input.ClientVersion), input.Replace)
	if errors.Is(err, storage.ErrDuplicateAccount) {
		writeAPIError(writer, http.StatusConflict, "duplicate_account", "this ChatGPT account already exists; repeat login with replace enabled to replace its router-owned credentials")
		return
	}
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "oauth_completion_failed", safeError(err))
		return
	}
	writeJSON(writer, http.StatusCreated, result)
}

func (server *Server) getCatalog(writer http.ResponseWriter, request *http.Request) {
	server.catalogResponse(writer, request, false)
}

func (server *Server) refreshCatalog(writer http.ResponseWriter, request *http.Request) {
	server.catalogResponse(writer, request, true)
}

func (server *Server) acknowledgeCatalogRestart(writer http.ResponseWriter, request *http.Request) {
	device := currentDevice(request.Context())
	server.status.Update(device.ID, func(status *routing.RouteStatus) {
		status.RestartRequired = false
	})
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) catalogResponse(writer http.ResponseWriter, request *http.Request, refresh bool) {
	device := currentDevice(request.Context())
	version := normalizedVersion(request.URL.Query().Get("codex_version"))
	if refresh {
		_ = server.RefreshProviders(request.Context())
		_ = server.accounts.RefreshAll(request.Context(), version)
	}
	result, err := server.catalog.BuildForDevice(request.Context(), device.ID, version)
	if err != nil {
		writeAPIError(writer, http.StatusServiceUnavailable, "catalog_unavailable", safeError(err))
		return
	}
	if request.Header.Get("If-None-Match") == `"`+result.Hash+`"` {
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	previous := server.status.Get(device.ID)
	now := time.Now().UTC()
	server.status.Update(device.ID, func(status *routing.RouteStatus) {
		status.RestartRequired = previous.CatalogHash != "" && previous.CatalogHash != result.Hash
		status.CatalogHash = result.Hash
		status.CatalogUpdated = now
	})
	_ = server.store.MarkCatalogSynced(request.Context(), device.ID)
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("ETag", `"`+result.Hash+`"`)
	writer.Header().Set("X-OpenCDX-Codex-Restart-Required", strconv.FormatBool(previous.CatalogHash != "" && previous.CatalogHash != result.Hash))
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(result.Raw)
}

func (server *Server) refreshQuotas(writer http.ResponseWriter, request *http.Request) {
	version := normalizedVersion(request.URL.Query().Get("codex_version"))
	if err := server.accounts.RefreshAll(request.Context(), version); err != nil {
		writeAPIError(writer, http.StatusBadGateway, "refresh_degraded", safeError(err))
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"status": "refreshed"})
}

func (server *Server) reconcileUsage(writer http.ResponseWriter, request *http.Request) {
	var snapshot usagehistory.Snapshot
	if !decodeJSONLimit(writer, request, &snapshot, 32<<20) {
		return
	}
	aggregates, err := validatedHistorySnapshot(snapshot, time.Now().UTC())
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_usage_history", err.Error())
		return
	}
	reconciledAt := time.Now().UTC()
	if err = server.store.ReplaceUsage(request.Context(), aggregates, storage.UsageReconciliation{
		ReconciledAt: reconciledAt, FilesScanned: snapshot.FilesScanned,
		EventsImported: snapshot.EventsImported, RowsImported: len(aggregates),
	}); err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "usage_reconciliation_failed", "usage history could not be stored")
		return
	}
	writeJSON(writer, http.StatusOK, usagehistory.Result{
		ReconciledAt: reconciledAt.Format(time.RFC3339), FilesScanned: snapshot.FilesScanned,
		EventsImported: snapshot.EventsImported, RowsImported: len(aggregates),
	})
}

func (server *Server) resetTelemetry(writer http.ResponseWriter, request *http.Request) {
	if err := server.store.ResetTelemetry(request.Context()); err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "telemetry_reset_failed", "telemetry could not be reset")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func validatedHistorySnapshot(snapshot usagehistory.Snapshot, now time.Time) ([]storage.UsageAggregate, error) {
	if snapshot.Version != usagehistory.SnapshotVersion {
		return nil, errors.New("usage history format is unsupported")
	}
	generatedAt, err := time.Parse(time.RFC3339, snapshot.GeneratedAt)
	if err != nil || generatedAt.After(now.Add(5*time.Minute)) || snapshot.DuplicateEvents < 0 || snapshot.MalformedLines < 0 {
		return nil, errors.New("usage history summary is invalid")
	}
	if snapshot.FilesScanned < 1 || snapshot.FilesScanned > 1_000_000 || snapshot.EventsImported < 1 || snapshot.EventsImported > 100_000_000 {
		return nil, errors.New("usage history summary is outside supported limits")
	}
	if len(snapshot.Rows) == 0 || len(snapshot.Rows) > 100_000 {
		return nil, errors.New("usage history contains no importable rows or is too large")
	}
	seen := make(map[string]struct{}, len(snapshot.Rows))
	aggregates := make([]storage.UsageAggregate, 0, len(snapshot.Rows))
	var requests int64
	for _, row := range snapshot.Rows {
		day, err := time.Parse("2006-01-02", row.Day)
		provider, model, routing := strings.TrimSpace(row.Provider), strings.TrimSpace(row.Model), strings.TrimSpace(row.Routing)
		if err != nil || day.Format("2006-01-02") != row.Day || day.After(now.Add(24*time.Hour)) {
			return nil, errors.New("usage history contains an invalid date")
		}
		if provider == "" || len(provider) > 100 || model == "" || len(model) > 512 || strings.ContainsAny(provider+model, "\x00\r\n") {
			return nil, errors.New("usage history contains an invalid provider or model")
		}
		if routing != usagehistory.RoutingRouted && routing != usagehistory.RoutingNative {
			return nil, errors.New("usage history contains an invalid routing classification")
		}
		if row.Requests < 1 || row.Requests > 100_000_000 || !validUsageCount(row.InputTokens) || !validUsageCount(row.CachedInputTokens) ||
			!validUsageCount(row.CacheWriteInputTokens) || !validUsageCount(row.OutputTokens) || !validUsageCount(row.ReasoningOutputTokens) {
			return nil, errors.New("usage history contains invalid counters")
		}
		key := row.Day + "\x00" + provider + "\x00" + model + "\x00" + routing
		if _, duplicate := seen[key]; duplicate {
			return nil, errors.New("usage history contains duplicate rows")
		}
		seen[key] = struct{}{}
		if requests > int64(snapshot.EventsImported)-row.Requests {
			return nil, errors.New("usage history request totals do not match")
		}
		requests += row.Requests
		aggregates = append(aggregates, storage.UsageAggregate{
			Day: row.Day, Provider: provider, ModelID: model, Routing: routing, Requests: row.Requests,
			InputTokens: row.InputTokens, CachedInputTokens: row.CachedInputTokens,
			CacheWriteInputTokens: row.CacheWriteInputTokens, OutputTokens: row.OutputTokens,
			ReasoningOutputTokens: row.ReasoningOutputTokens,
		})
	}
	if requests != int64(snapshot.EventsImported) {
		return nil, errors.New("usage history request totals do not match")
	}
	return aggregates, nil
}

func validUsageCount(value int64) bool { return value >= 0 && value <= 1_000_000_000_000_000 }

func (server *Server) responses(writer http.ResponseWriter, request *http.Request) {
	device := currentDevice(request.Context())
	server.proxy.ServeDeviceHTTP(writer, request, routing.DeviceContext{ID: device.ID})
}

func (server *Server) RefreshProviders(ctx context.Context) error {
	var failures []string
	if provider, err := server.store.Provider(ctx, "openrouter", true); err == nil && provider.Enabled {
		var clientErr error
		if provider.APIKey == "" {
			clientErr = errors.New("OpenRouter API key is required")
		}
		client, newErr := openrouter.New(server.httpClient, provider.BaseURL, provider.APIKey)
		if clientErr == nil {
			clientErr = newErr
		}
		if clientErr == nil {
			clientErr = server.catalog.RefreshOpenRouter(ctx, client)
		}
		if clientErr == nil {
			clientErr = server.catalog.ValidateProviderCatalog(ctx, "openrouter")
		}
		if clientErr != nil {
			failures = append(failures, "OpenRouter")
			_ = server.store.SetProviderHealth(ctx, "openrouter", "error", safeError(clientErr))
		} else {
			_ = server.store.SetProviderHealth(ctx, "openrouter", "healthy", "")
		}
	}
	if provider, err := server.store.Provider(ctx, "ollama", false); err == nil && provider.Enabled {
		client, clientErr := ollama.New(server.httpClient, provider.BaseURL, server.insecureDev || provider.AllowHTTP())
		if clientErr == nil {
			clientErr = server.catalog.RefreshOllama(ctx, client)
		}
		if clientErr == nil {
			clientErr = server.catalog.ValidateProviderCatalog(ctx, "ollama")
		}
		if clientErr != nil {
			failures = append(failures, "Ollama")
			_ = server.store.SetProviderHealth(ctx, "ollama", "error", safeError(clientErr))
		} else {
			_ = server.store.SetProviderHealth(ctx, "ollama", "healthy", "")
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("provider refresh failed: %s", strings.Join(failures, ", "))
	}
	return nil
}

func (server *Server) loginPage(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.session(request); ok {
		http.Redirect(writer, request, "/admin", http.StatusSeeOther)
		return
	}
	_ = server.templates.ExecuteTemplate(writer, "login.html", map[string]string{
		"Error": request.URL.Query().Get("error"), "AssetVersion": assetVersion(),
	})
}

func (server *Server) login(writer http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil || !constantEqual(request.FormValue("token"), server.adminSecret) {
		http.Redirect(writer, request, "/admin/login?error=Invalid+administrator+token", http.StatusSeeOther)
		return
	}
	token, err := secure.RandomURLSafe(36)
	if err != nil {
		http.Error(writer, "session creation failed", http.StatusInternalServerError)
		return
	}
	csrf, err := secure.RandomURLSafe(24)
	if err != nil {
		http.Error(writer, "session creation failed", http.StatusInternalServerError)
		return
	}
	server.sessionsMu.Lock()
	server.sessions[tokenHash(token)] = adminSession{CSRF: csrf, ExpiresAt: time.Now().UTC().Add(sessionLifetime)}
	server.sessionsMu.Unlock()
	http.SetCookie(writer, &http.Cookie{Name: "opencdx_admin", Value: token, Path: "/admin", HttpOnly: true, Secure: server.publicURL.Scheme == "https", SameSite: http.SameSiteStrictMode, MaxAge: int(sessionLifetime.Seconds())})
	http.Redirect(writer, request, "/admin", http.StatusSeeOther)
}

func (server *Server) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		session, ok := server.session(request)
		if !ok {
			http.Redirect(writer, request, "/admin/login", http.StatusSeeOther)
			return
		}
		if request.Method != http.MethodGet {
			if err := request.ParseForm(); err != nil || !constantEqual(request.FormValue("csrf"), session.CSRF) {
				http.Error(writer, "invalid CSRF token", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func (server *Server) session(request *http.Request) (adminSession, bool) {
	cookie, err := request.Cookie("opencdx_admin")
	if err != nil {
		return adminSession{}, false
	}
	now := time.Now().UTC()
	server.sessionsMu.Lock()
	defer server.sessionsMu.Unlock()
	for key, session := range server.sessions {
		if !now.Before(session.ExpiresAt) {
			delete(server.sessions, key)
		}
	}
	session, ok := server.sessions[tokenHash(cookie.Value)]
	return session, ok && now.Before(session.ExpiresAt)
}

func (server *Server) logout(writer http.ResponseWriter, request *http.Request) {
	if cookie, err := request.Cookie("opencdx_admin"); err == nil {
		server.sessionsMu.Lock()
		delete(server.sessions, tokenHash(cookie.Value))
		server.sessionsMu.Unlock()
	}
	http.SetCookie(writer, &http.Cookie{Name: "opencdx_admin", Value: "", Path: "/admin", HttpOnly: true, Secure: server.publicURL.Scheme == "https", SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.Redirect(writer, request, "/admin/login", http.StatusSeeOther)
}

func (server *Server) dashboard(writer http.ResponseWriter, request *http.Request) {
	session, _ := server.session(request)
	page, err := server.dashboardData(request.Context(), session.CSRF)
	if err != nil {
		http.Error(writer, "dashboard data unavailable", http.StatusInternalServerError)
		return
	}
	page.Message = request.URL.Query().Get("message")
	page.MessageError = request.URL.Query().Get("error") == "1"
	var rendered bytes.Buffer
	if err = server.templates.ExecuteTemplate(&rendered, "dashboard.html", page); err != nil {
		http.Error(writer, "dashboard rendering failed", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = writer.Write(rendered.Bytes())
}

func (server *Server) adminTelemetry(writer http.ResponseWriter, request *http.Request) {
	now := time.Now().UTC()
	usage, err := server.store.Usage(request.Context(), time.Time{})
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "telemetry_unavailable", "aggregate telemetry is unavailable")
		return
	}
	var reconciliation *storage.UsageReconciliation
	metadata, metadataErr := server.store.UsageReconciliation(request.Context())
	if metadataErr == nil {
		reconciliation = &metadata
	} else if !errors.Is(metadataErr, storage.ErrNotFound) {
		writeAPIError(writer, http.StatusInternalServerError, "telemetry_unavailable", "reconciliation metadata is unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, telemetry.Build(usage, reconciliation, now))
}

func (server *Server) adminResetTelemetry(writer http.ResponseWriter, request *http.Request) {
	err := server.store.ResetTelemetry(request.Context())
	message := "Telemetry reset; providers, devices, accounts, and local Codex history were not changed"
	if err != nil {
		message = "Telemetry could not be reset"
	}
	redirectMessage(writer, request, message, err != nil)
}

func (server *Server) adminRefresh(writer http.ResponseWriter, request *http.Request) {
	providerErr := server.RefreshProviders(request.Context())
	accountErr := server.accounts.RefreshAll(request.Context(), normalizedVersion(""))
	if providerErr != nil || accountErr != nil {
		redirectMessage(writer, request, "Refresh completed with degraded providers or accounts", true)
		return
	}
	redirectMessage(writer, request, "Quotas and provider catalogs refreshed", false)
}

func (server *Server) adminAccount(writer http.ResponseWriter, request *http.Request) {
	id, action := request.PathValue("id"), request.PathValue("action")
	var err error
	switch action {
	case "pause":
		err = server.store.SetAccountPaused(request.Context(), id, true)
	case "resume":
		err = server.store.SetAccountPaused(request.Context(), id, false)
	case "primary":
		err = server.store.SetPrimaryAccount(request.Context(), id)
	case "delete":
		err = server.store.DeleteAccount(request.Context(), id)
	default:
		err = errors.New("unknown account action")
	}
	redirectMessage(writer, request, map[bool]string{true: "Account action failed", false: "Account updated"}[err != nil], err != nil)
}

func (server *Server) adminAccountOrder(writer http.ResponseWriter, request *http.Request) {
	err := server.store.ReorderAccounts(request.Context(), request.Form["account_id"])
	redirectMessage(writer, request, map[bool]string{true: "Account priority update failed", false: "Account priority updated"}[err != nil], err != nil)
}

func (server *Server) adminDevice(writer http.ResponseWriter, request *http.Request) {
	id, action := request.PathValue("id"), request.PathValue("action")
	var err error
	message := "Device updated"
	switch action {
	case "approve":
		err = server.store.ApproveDevice(request.Context(), id)
	case "reject":
		err = server.store.RejectDevice(request.Context(), id)
	case "revoke":
		err = server.store.RevokeDevice(request.Context(), id)
		if err == nil {
			server.status.Delete(id)
			message = "Device removed"
		}
	case "delete":
		err = server.store.DeleteDevice(request.Context(), id)
		if err == nil {
			server.status.Delete(id)
			message = "Device deleted"
		}
	default:
		err = errors.New("unknown device action")
	}
	if err != nil {
		message = "Device action failed"
	}
	redirectMessage(writer, request, message, err != nil)
}

func (server *Server) adminProvider(writer http.ResponseWriter, request *http.Request) {
	name := request.PathValue("name")
	if name != "openrouter" && name != "ollama" {
		redirectMessage(writer, request, "Unknown provider", true)
		return
	}
	provider := storage.ProviderConfig{Name: name, BaseURL: strings.TrimSpace(request.FormValue("base_url")), APIKey: strings.TrimSpace(request.FormValue("api_key")), Enabled: request.FormValue("enabled") == "on", Health: "disabled"}
	if name == "ollama" {
		provider.Config, _ = json.Marshal(struct {
			AllowHTTP bool `json:"allow_http"`
		}{AllowHTTP: request.FormValue("allow_http") == "on"})
	}
	if provider.Enabled {
		provider.Health = "checking"
	}
	var validationErr error
	if name == "openrouter" {
		effectiveKey := provider.APIKey
		if effectiveKey == "" {
			if existing, existingErr := server.store.Provider(request.Context(), name, true); existingErr == nil {
				effectiveKey = existing.APIKey
			}
		}
		if provider.Enabled && effectiveKey == "" {
			validationErr = errors.New("an OpenRouter API key is required when the provider is enabled")
		} else {
			_, validationErr = openrouter.New(server.httpClient, provider.BaseURL, effectiveKey)
		}
	} else {
		_, validationErr = ollama.New(server.httpClient, provider.BaseURL, server.insecureDev || provider.AllowHTTP())
	}
	if validationErr != nil {
		redirectMessage(writer, request, validationErr.Error(), true)
		return
	}
	if err := server.store.PutProvider(request.Context(), provider); err != nil {
		redirectMessage(writer, request, "Provider could not be saved", true)
		return
	}
	if provider.Enabled {
		if err := server.RefreshProviders(request.Context()); err != nil {
			redirectMessage(writer, request, "Provider saved, but validation failed", true)
			return
		}
	}
	redirectMessage(writer, request, "Provider configuration saved", false)
}

func (server *Server) adminProviderRemoveSecret(writer http.ResponseWriter, request *http.Request) {
	err := server.store.ClearProviderSecret(request.Context(), request.PathValue("name"))
	redirectMessage(writer, request, map[bool]string{true: "Credential removal failed", false: "Provider credential removed"}[err != nil], err != nil)
}

type dashboardPage struct {
	CSRF                    string
	AssetVersion            string
	Message                 string
	MessageError            bool
	RepositoryURL           string
	CurrentVersion          string
	LatestVersion           string
	UpdateAvailable         bool
	Accounts                []accountView
	ReadyAccountCount       int
	AccountsHealthy         bool
	PrimaryAccountEmail     string
	PrimaryAccountPlan      string
	NearestResetDate        string
	NearestResetTime        string
	NearestResetAt          string
	Providers               []providerView
	ConfiguredProviderCount int
	HealthyProviderCount    int
	ProviderModelCount      int
	ProvidersChecked        string
	ProvidersCheckedAt      string
	Devices                 []deviceView
	Models                  []modelView
	Conflicts               []conflictView
	AvailableModelCount     int
	ExcludedModelCount      int
}

type accountView struct {
	ID, MaskedEmail, Plan, Status, LastError string
	Paused, Primary                          bool
	ResetCredits                             int
	VisibleModels, MoreModels                []string
	Quotas                                   []quotaView
}

type quotaView struct {
	Name, Reset, ResetAt string
	Remaining            float64
	Spark                bool
}
type deviceView struct {
	ID, Name, Status, LastSeen, LastSeenAt, CatalogSynced, CatalogSyncedAt string
	Laptop                                                                 bool
}
type providerView struct {
	Name, DisplayName, Description, BaseURL, Health, LastError, Updated, UpdatedAt string
	Enabled, HasCredential, AllowHTTP                                              bool
}
type conflictView struct{ Model, Detail string }
type modelView struct{ Provider, Model, State, Detail string }

func (server *Server) dashboardData(ctx context.Context, csrf string) (dashboardPage, error) {
	update := appversion.UpdateStatus{Current: appversion.Display()}
	if server.updates != nil {
		update = server.updates.Snapshot()
	}
	page := dashboardPage{
		CSRF: csrf, AccountsHealthy: true, RepositoryURL: appversion.RepositoryURL,
		AssetVersion:   assetVersion(),
		CurrentVersion: update.Current, LatestVersion: update.Latest, UpdateAvailable: update.Available,
	}
	accounts, err := server.store.Accounts(ctx, false)
	if err != nil {
		return page, err
	}
	var nearestReset time.Time
	considerReset := func(candidate time.Time) {
		if candidate.IsZero() || (!nearestReset.IsZero() && !candidate.Before(nearestReset)) {
			return
		}
		nearestReset = candidate
	}
	for _, account := range accounts {
		remaining := maxFloat(0, 100-account.QuotaUsedPercent)
		view := accountView{
			ID: account.ID, MaskedEmail: account.MaskedEmail, Plan: account.Plan, Status: account.Status,
			Paused: account.Paused, Primary: account.Primary, ResetCredits: account.ResetCredits, LastError: account.LastError,
		}
		primaryQuota := quotaView{Name: "Codex", Remaining: remaining}
		if !account.QuotaResetAt.IsZero() {
			primaryQuota.Reset = account.QuotaResetAt.Local().Format("Jan 2 · 15:04")
			primaryQuota.ResetAt = browserTimestamp(account.QuotaResetAt)
			considerReset(account.QuotaResetAt)
		}
		view.Quotas = append(view.Quotas, primaryQuota)
		if additional, parseErr := openai.ParseAdditionalQuotas(account.RawQuota); parseErr == nil {
			for _, quota := range additional {
				identity := strings.ToLower(quota.Name + " " + quota.MeteredFeature)
				if !strings.Contains(identity, "spark") {
					continue
				}
				spark := quotaView{Name: "Codex Spark", Remaining: maxFloat(0, 100-quota.UsedPercent), Spark: true}
				if !quota.ResetAt.IsZero() {
					spark.Reset = quota.ResetAt.Local().Format("Jan 2 · 15:04")
					spark.ResetAt = browserTimestamp(quota.ResetAt)
					considerReset(quota.ResetAt)
				}
				view.Quotas = append(view.Quotas, spark)
			}
		}
		models := append([]string(nil), account.EntitledModels...)
		const visibleModelPills = 10
		if len(models) > visibleModelPills {
			view.VisibleModels = models[:visibleModelPills]
			view.MoreModels = models[visibleModelPills:]
		} else {
			view.VisibleModels = models
		}
		page.Accounts = append(page.Accounts, view)
		if account.Status == "ready" && !account.Paused {
			page.ReadyAccountCount++
		}
		if account.Status != "ready" {
			page.AccountsHealthy = false
		}
		if account.Primary {
			page.PrimaryAccountEmail = account.MaskedEmail
			page.PrimaryAccountPlan = account.Plan
		}
	}
	if len(accounts) == 0 {
		page.AccountsHealthy = false
	}
	if !nearestReset.IsZero() {
		page.NearestResetDate = nearestReset.Local().Format("Jan 2")
		page.NearestResetTime = nearestReset.Local().Format("15:04")
		page.NearestResetAt = browserTimestamp(nearestReset)
	}
	providerConfigs, err := server.store.Providers(ctx)
	if err != nil {
		return page, err
	}
	var latestProviderCheck time.Time
	for _, provider := range providerConfigs {
		view := providerView{
			Name: provider.Name, BaseURL: provider.BaseURL, Enabled: provider.Enabled, Health: provider.Health,
			LastError: provider.LastError, Updated: friendlyTime(provider.UpdatedAt), UpdatedAt: browserTimestamp(provider.UpdatedAt),
			AllowHTTP: provider.AllowHTTP(),
		}
		switch provider.Name {
		case "ollama":
			view.DisplayName, view.Description = "Ollama", "Local or remote Ollama API"
		case "openrouter":
			view.DisplayName, view.Description = "OpenRouter", "OpenRouter API"
		default:
			view.DisplayName, view.Description = provider.Name, "Model provider"
		}
		if hasCredential, secretErr := server.store.ProviderHasSecret(ctx, provider.Name); secretErr == nil {
			view.HasCredential = hasCredential
		}
		page.Providers = append(page.Providers, view)
		if provider.Enabled {
			page.ConfiguredProviderCount++
		}
		if provider.Health == "healthy" {
			page.HealthyProviderCount++
		}
		if provider.UpdatedAt.After(latestProviderCheck) {
			latestProviderCheck = provider.UpdatedAt
		}
	}
	if !latestProviderCheck.IsZero() {
		page.ProvidersChecked = friendlyTime(latestProviderCheck)
		page.ProvidersCheckedAt = browserTimestamp(latestProviderCheck)
	}
	devices, err := server.store.Devices(ctx)
	if err != nil {
		return page, err
	}
	for _, device := range devices {
		name := strings.ToLower(device.Name)
		page.Devices = append(page.Devices, deviceView{
			ID: device.ID, Name: device.Name, Status: device.Status,
			LastSeen: friendlyTime(device.LastSeenAt), LastSeenAt: browserTimestamp(device.LastSeenAt),
			CatalogSynced: friendlyTime(device.CatalogSynced), CatalogSyncedAt: browserTimestamp(device.CatalogSynced),
			Laptop: strings.Contains(name, "book") || strings.Contains(name, "laptop"),
		})
	}
	exclusions, err := server.store.Exclusions(ctx)
	if err != nil {
		return page, err
	}
	page.Models = server.discoveredModels(ctx, exclusions)
	for _, model := range page.Models {
		if model.Provider != "openai" {
			page.ProviderModelCount++
		}
		if model.State == "available" {
			page.AvailableModelCount++
		} else {
			page.ExcludedModelCount++
		}
	}
	conflicts, err := server.store.Conflicts(ctx)
	if err != nil {
		return page, err
	}
	for model, detail := range conflicts {
		page.Conflicts = append(page.Conflicts, conflictView{Model: model, Detail: detail})
	}
	sort.Slice(page.Conflicts, func(left, right int) bool { return page.Conflicts[left].Model < page.Conflicts[right].Model })
	return page, nil
}

func (server *Server) discoveredModels(ctx context.Context, exclusions []storage.CatalogExclusion) []modelView {
	excluded := make(map[string]string)
	for _, exclusion := range exclusions {
		excluded[exclusion.Provider+"\x00"+exclusion.ModelID] = exclusion.Reason
	}
	seen := make(map[string]bool)
	var models []modelView
	appendModel := func(provider, model string) {
		key := provider + "\x00" + model
		if model == "" || seen[key] {
			return
		}
		seen[key] = true
		state, detail := "available", ""
		if reason := excluded[key]; reason != "" {
			state, detail = "excluded", reason
		}
		models = append(models, modelView{Provider: provider, Model: model, State: state, Detail: detail})
	}
	if snapshots, err := server.store.CatalogSnapshots(ctx, "openai"); err == nil {
		for _, snapshot := range snapshots {
			if discovery, parseErr := openai.ParseNativeModels(snapshot.Raw); parseErr == nil {
				for _, model := range discovery {
					appendModel("openai", model.ID)
				}
			}
		}
	}
	if snapshots, err := server.store.CatalogSnapshots(ctx, "openrouter"); err == nil && len(snapshots) > 0 {
		if discovery, parseErr := catalog.ParseOpenRouterSnapshot(snapshots[0].Raw); parseErr == nil {
			for _, model := range discovery.Models {
				appendModel("openrouter", model.ID)
			}
		}
	}
	if snapshots, err := server.store.CatalogSnapshots(ctx, "ollama"); err == nil && len(snapshots) > 0 {
		if discovery, parseErr := catalog.ParseTranslatedSnapshot(snapshots[0].Raw); parseErr == nil {
			for _, model := range discovery.Models {
				appendModel("ollama", model.ID)
			}
		}
	}
	sort.Slice(models, func(left, right int) bool {
		if models[left].Provider == models[right].Provider {
			return models[left].Model < models[right].Model
		}
		return models[left].Provider < models[right].Provider
	})
	return models
}

func (server *Server) safeAccounts(ctx context.Context) ([]map[string]any, error) {
	accounts, err := server.store.Accounts(ctx, false)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		result = append(result, map[string]any{"masked_email": account.MaskedEmail, "plan": account.Plan, "status": account.Status, "paused": account.Paused, "primary": account.Primary, "quota_remaining": maxFloat(0, 100-account.QuotaUsedPercent), "quota_reset_at": account.QuotaResetAt, "reset_credits": account.ResetCredits, "models": len(account.EntitledModels)})
	}
	return result, nil
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) bool {
	return decodeJSONLimit(writer, request, destination, 1<<20)
}

func decodeJSONLimit(writer http.ResponseWriter, request *http.Request, destination any, limit int64) bool {
	decoder := json.NewDecoder(io.LimitReader(request.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_json", "request body is invalid")
		return false
	}
	return true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeAPIError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, map[string]any{"error": map[string]string{"type": code, "message": message}})
}

func redirectMessage(writer http.ResponseWriter, request *http.Request, message string, isError bool) {
	values := url.Values{"message": {message}}
	if isError {
		values.Set("error", "1")
	}
	tab := request.FormValue("return_tab")
	switch tab {
	case "home", "accounts", "providers", "devices", "catalog":
	default:
		tab = "home"
	}
	http.Redirect(writer, request, "/admin?"+values.Encode()+"#"+tab, http.StatusSeeOther)
}

func currentDevice(ctx context.Context) storage.Device {
	device, _ := ctx.Value(deviceContextKey{}).(storage.Device)
	return device
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func constantEqual(left, right string) bool {
	leftHash, rightHash := sha256.Sum256([]byte(left)), sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}

func tokenHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func normalizedVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 80 {
		return "0.0.0"
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return "0.0.0"
	}
	for _, part := range parts {
		if number, err := strconv.Atoi(part); err != nil || number < 0 {
			return "0.0.0"
		}
	}
	return value
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, word := range []string{"token", "credential", "bearer", "api key", "authorization code", "account id"} {
		if strings.Contains(strings.ToLower(message), word) {
			return "provider authentication or validation failed"
		}
	}
	if len(message) > 300 {
		return "operation failed"
	}
	return message
}

func friendlyTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.Local().Format("Jan 2 15:04")
}

func browserTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
