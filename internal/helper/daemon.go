package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LocalStatus struct {
	State           string             `json:"state"`
	Connected       bool               `json:"connected"`
	ActiveRequests  int                `json:"active_requests"`
	RouterURL       string             `json:"router_url"`
	DeviceName      string             `json:"device_name"`
	Accounts        []AccountAllowance `json:"accounts,omitempty"`
	Provider        string             `json:"provider,omitempty"`
	Model           string             `json:"model,omitempty"`
	Account         string             `json:"account,omitempty"`
	QuotaRemaining  float64            `json:"quota_remaining,omitempty"`
	QuotaResetAt    *time.Time         `json:"quota_reset_at,omitempty"`
	CatalogSynced   bool               `json:"catalog_synced"`
	CatalogUpdated  *time.Time         `json:"catalog_updated_at,omitempty"`
	RestartRequired bool               `json:"codex_restart_required,omitempty"`
	LastRequestAt   *time.Time         `json:"last_request_at,omitempty"`
	LastError       string             `json:"last_error,omitempty"`
}

type AccountAllowance struct {
	MaskedEmail    string     `json:"masked_email"`
	Plan           string     `json:"plan,omitempty"`
	Status         string     `json:"status"`
	Paused         bool       `json:"paused"`
	Primary        bool       `json:"primary,omitempty"`
	QuotaRemaining float64    `json:"quota_remaining"`
	QuotaResetAt   *time.Time `json:"quota_reset_at,omitempty"`
	ResetCredits   int        `json:"reset_credits,omitempty"`
}

type Daemon struct {
	configPath     string
	catalogPath    string
	config         Config
	secrets        SecretStore
	localSecret    string
	deviceToken    string
	remote         *RemoteClient
	statusMu       sync.RWMutex
	status         LocalStatus
	remoteStatusMu sync.Mutex
	catalogMu      sync.Mutex
	shutdownOnce   sync.Once
	shutdown       chan struct{}
	server         *http.Server
}

func NewDaemon(configPath string, config Config, secrets SecretStore) (*Daemon, error) {
	localSecret, err := secrets.Get("local-token-secret")
	if err != nil {
		return nil, errors.New("local token secret is missing; run enroll again")
	}
	deviceToken, err := secrets.Get("device-token")
	if err != nil {
		return nil, errors.New("device credential is missing; complete enrollment first")
	}
	remote, err := NewRemoteClient(config, deviceToken)
	if err != nil {
		return nil, err
	}
	return &Daemon{
		configPath: configPath, catalogPath: config.CatalogPath, config: config, secrets: secrets, localSecret: localSecret,
		deviceToken: deviceToken, remote: remote, shutdown: make(chan struct{}),
		status: LocalStatus{State: "connecting", RouterURL: config.RouterURL, DeviceName: config.DeviceName, CatalogSynced: CatalogExists(config)},
	}, nil
}

func (daemon *Daemon) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", daemon.config.ListenPort))
	if err != nil {
		return fmt.Errorf("start loopback helper: %w", err)
	}
	mux := http.NewServeMux()
	proxy := daemon.trackInferenceActivity(daemon.responsesProxy())
	mux.Handle("POST /v1/responses", daemon.localAuth(proxy))
	mux.Handle("POST /v1/responses/compact", daemon.localAuth(proxy))
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeHelperJSON(writer, http.StatusOK, map[string]any{
			"status":          "ok",
			"active_requests": daemon.currentStatus().ActiveRequests,
		})
	})
	mux.Handle("GET /control/status", daemon.controlAuth(http.HandlerFunc(daemon.controlStatus)))
	mux.Handle("POST /control/reconnect", daemon.controlAuth(http.HandlerFunc(daemon.controlReconnect)))
	mux.Handle("POST /control/catalog/refresh", daemon.controlAuth(http.HandlerFunc(daemon.controlCatalogRefresh)))
	mux.Handle("POST /control/catalog/restart-ack", daemon.controlAuth(http.HandlerFunc(daemon.controlCatalogRestartAck)))
	mux.Handle("POST /control/quotas/refresh", daemon.controlAuth(http.HandlerFunc(daemon.controlQuotaRefresh)))
	mux.Handle("POST /control/quit", daemon.controlAuth(http.HandlerFunc(daemon.controlQuit)))
	daemon.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 0, WriteTimeout: 0, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- daemon.server.Serve(listener) }()
	go daemon.syncLoop(ctx)
	select {
	case <-ctx.Done():
	case <-daemon.shutdown:
	case err = <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return daemon.server.Shutdown(shutdownContext)
}

func (daemon *Daemon) responsesProxy() http.Handler {
	target, _ := url.Parse(daemon.config.RouterURL)
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment, MaxIdleConns: 20, MaxIdleConnsPerHost: 10,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 15 * time.Second,
		ExpectContinueTimeout: time.Second, ResponseHeaderTimeout: 5 * time.Minute,
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Header.Set("Authorization", "Bearer "+daemon.deviceToken)
			request.Out.Header.Del("X-OpenCDX-Control")
		},
		Transport: transport, FlushInterval: -1,
		ModifyResponse: func(response *http.Response) error {
			daemon.observeRouteHeaders(response.Header)
			for name := range response.Header {
				if strings.HasPrefix(strings.ToLower(name), "x-opencdx-") {
					response.Header.Del(name)
				}
			}
			return nil
		},
		ErrorHandler: daemon.handleProxyError,
	}
	return proxy
}

func (daemon *Daemon) handleProxyError(writer http.ResponseWriter, request *http.Request, _ error) {
	// ReverseProxy reports a cancelled local request through ErrorHandler when
	// Codex stops reading or its context expires. That says nothing about the
	// remote router's health, and the downstream connection is already gone.
	if request.Context().Err() != nil {
		return
	}
	daemon.updateStatus(func(status *LocalStatus) {
		status.Connected = false
		status.State = "error"
		status.LastError = "remote router is unreachable"
	})
	writeHelperJSON(writer, http.StatusBadGateway, map[string]any{"error": map[string]string{"type": "router_unreachable", "message": "remote router is unreachable"}})
}

func (daemon *Daemon) trackInferenceActivity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		daemon.updateStatus(func(status *LocalStatus) { status.ActiveRequests++ })
		defer daemon.updateStatus(func(status *LocalStatus) {
			if status.ActiveRequests > 0 {
				status.ActiveRequests--
			}
		})
		next.ServeHTTP(writer, request)
	})
}

func (daemon *Daemon) localAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		parts := strings.Fields(request.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !VerifyLocalToken(daemon.localSecret, parts[1], time.Now().UTC()) {
			writer.Header().Set("WWW-Authenticate", "Bearer")
			writeHelperJSON(writer, http.StatusUnauthorized, map[string]any{"error": map[string]string{"type": "invalid_local_token", "message": "local helper authentication failed"}})
			return
		}
		request.Header.Del("Authorization")
		next.ServeHTTP(writer, request)
	})
}

func (daemon *Daemon) controlAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !constantStringEqual(request.Header.Get("X-OpenCDX-Control"), daemon.localSecret) {
			http.Error(writer, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (daemon *Daemon) controlStatus(writer http.ResponseWriter, _ *http.Request) {
	// login-openai runs as a short-lived sibling process and can write the
	// catalog while this daemon is already running. Reflect the file immediately
	// instead of leaving the menu at Pending until the next catalog poll.
	if CatalogExists(Config{CatalogPath: daemon.catalogPath}) {
		daemon.updateStatus(func(status *LocalStatus) { status.CatalogSynced = true })
	}
	writeHelperJSON(writer, http.StatusOK, daemon.currentStatus())
}

func (daemon *Daemon) controlReconnect(writer http.ResponseWriter, request *http.Request) {
	if err := daemon.refreshStatus(request.Context()); err != nil {
		writeHelperJSON(writer, http.StatusBadGateway, daemon.currentStatus())
		return
	}
	writeHelperJSON(writer, http.StatusOK, daemon.currentStatus())
}

func (daemon *Daemon) controlCatalogRefresh(writer http.ResponseWriter, request *http.Request) {
	result, err := daemon.syncCatalog(request.Context(), true)
	if err != nil {
		daemon.updateStatus(func(status *LocalStatus) { status.State = "degraded"; status.LastError = err.Error() })
		writeHelperJSON(writer, http.StatusBadGateway, daemon.currentStatus())
		return
	}
	daemon.recordCatalogResult(result)
	_ = daemon.refreshStatus(request.Context())
	writeHelperJSON(writer, http.StatusOK, daemon.currentStatus())
}

func (daemon *Daemon) controlCatalogRestartAck(writer http.ResponseWriter, request *http.Request) {
	// Serialize acknowledgement with both catalog and status requests so an
	// older in-flight response cannot immediately restore the reminder.
	daemon.catalogMu.Lock()
	defer daemon.catalogMu.Unlock()
	daemon.remoteStatusMu.Lock()
	defer daemon.remoteStatusMu.Unlock()
	_, err := daemon.remote.JSON(request.Context(), http.MethodPost, "/api/v1/catalog/restart-ack", nil, nil, true)
	if err != nil {
		writeHelperJSON(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	daemon.updateStatus(func(status *LocalStatus) {
		status.RestartRequired = false
	})
	writeHelperJSON(writer, http.StatusOK, daemon.currentStatus())
}

func (daemon *Daemon) controlQuotaRefresh(writer http.ResponseWriter, request *http.Request) {
	_, err := daemon.remote.JSON(request.Context(), http.MethodPost, "/api/v1/quotas/refresh?codex_version="+url.QueryEscape(DetectCodexVersion(request.Context())), nil, nil, true)
	if err != nil {
		writeHelperJSON(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	_ = daemon.refreshStatus(request.Context())
	writeHelperJSON(writer, http.StatusOK, daemon.currentStatus())
}

func (daemon *Daemon) controlQuit(writer http.ResponseWriter, _ *http.Request) {
	writeHelperJSON(writer, http.StatusOK, map[string]string{"status": "stopping"})
	daemon.shutdownOnce.Do(func() { close(daemon.shutdown) })
}

func (daemon *Daemon) syncLoop(ctx context.Context) {
	_ = daemon.refreshStatus(ctx)
	if result, err := daemon.syncCatalog(ctx, false); err == nil {
		daemon.recordCatalogResult(result)
	}
	statusTicker := time.NewTicker(15 * time.Second)
	catalogTicker := time.NewTicker(time.Minute)
	defer statusTicker.Stop()
	defer catalogTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-daemon.shutdown:
			return
		case <-statusTicker.C:
			_ = daemon.refreshStatus(ctx)
		case <-catalogTicker.C:
			result, err := daemon.syncCatalog(ctx, false)
			if err == nil {
				daemon.recordCatalogResult(result)
			}
		}
	}
}

func (daemon *Daemon) recordCatalogResult(result CatalogResult) {
	daemon.updateStatus(func(status *LocalStatus) {
		status.CatalogSynced = true
		status.CatalogUpdated = timePointer(time.Now().UTC())
		// An unchanged (304) catalog is not evidence that Codex reloaded the
		// last changed file. Keep the reminder until the user acknowledges it.
		status.RestartRequired = status.RestartRequired || result.RestartRequired
	})
}

func (daemon *Daemon) syncCatalog(ctx context.Context, force bool) (CatalogResult, error) {
	daemon.catalogMu.Lock()
	defer daemon.catalogMu.Unlock()
	return SyncCatalog(ctx, daemon.remote, daemon.configPath, &daemon.config, DetectCodexVersion(ctx), force)
}

func (daemon *Daemon) refreshStatus(ctx context.Context) error {
	daemon.remoteStatusMu.Lock()
	defer daemon.remoteStatusMu.Unlock()
	var remoteStatus struct {
		Accounts []struct {
			MaskedEmail    string    `json:"masked_email"`
			Plan           string    `json:"plan"`
			Status         string    `json:"status"`
			Paused         bool      `json:"paused"`
			Primary        bool      `json:"primary"`
			QuotaRemaining float64   `json:"quota_remaining"`
			QuotaResetAt   time.Time `json:"quota_reset_at"`
			ResetCredits   int       `json:"reset_credits"`
		} `json:"accounts"`
		Route struct {
			Connected       bool      `json:"connected"`
			State           string    `json:"state"`
			Provider        string    `json:"provider"`
			Model           string    `json:"model"`
			Account         string    `json:"account"`
			QuotaRemaining  float64   `json:"quota_remaining"`
			QuotaResetAt    time.Time `json:"quota_reset_at"`
			RestartRequired bool      `json:"codex_restart_required"`
			LastRequestAt   time.Time `json:"last_request_at"`
			LastError       string    `json:"last_error"`
		} `json:"route"`
	}
	_, err := daemon.remote.JSON(ctx, http.MethodGet, "/api/v1/device/status", nil, &remoteStatus, true)
	if err != nil {
		daemon.updateStatus(func(status *LocalStatus) {
			status.Connected = false
			status.State = "error"
			status.LastError = err.Error()
		})
		return err
	}
	daemon.updateStatus(func(status *LocalStatus) {
		status.Connected = true
		status.State = remoteStatus.Route.State
		if status.State == "" {
			status.State = "connected"
		}
		status.Provider, status.Model, status.Account = remoteStatus.Route.Provider, remoteStatus.Route.Model, remoteStatus.Route.Account
		status.QuotaRemaining = remoteStatus.Route.QuotaRemaining
		status.QuotaResetAt = nonZeroTimePointer(remoteStatus.Route.QuotaResetAt)
		status.Accounts = make([]AccountAllowance, 0, len(remoteStatus.Accounts))
		for _, account := range remoteStatus.Accounts {
			status.Accounts = append(status.Accounts, AccountAllowance{
				MaskedEmail: account.MaskedEmail, Plan: account.Plan, Status: account.Status,
				Paused: account.Paused, Primary: account.Primary, QuotaRemaining: account.QuotaRemaining,
				QuotaResetAt: nonZeroTimePointer(account.QuotaResetAt), ResetCredits: account.ResetCredits,
			})
		}
		status.RestartRequired = status.RestartRequired || remoteStatus.Route.RestartRequired
		status.LastRequestAt, status.LastError = nonZeroTimePointer(remoteStatus.Route.LastRequestAt), remoteStatus.Route.LastError
	})
	return nil
}

func (daemon *Daemon) observeRouteHeaders(headers http.Header) {
	quota, _ := strconv.ParseFloat(headers.Get("X-OpenCDX-Quota-Remaining"), 64)
	reset, _ := time.Parse(time.RFC3339, headers.Get("X-OpenCDX-Quota-Reset"))
	daemon.updateStatus(func(status *LocalStatus) {
		status.Connected, status.State = true, "connected"
		status.Provider = headers.Get("X-OpenCDX-Provider")
		status.Model = headers.Get("X-OpenCDX-Model")
		status.Account = headers.Get("X-OpenCDX-Account")
		status.QuotaRemaining, status.QuotaResetAt = quota, nonZeroTimePointer(reset)
		status.LastRequestAt, status.LastError = timePointer(time.Now().UTC()), ""
	})
}

func (daemon *Daemon) updateStatus(update func(*LocalStatus)) {
	daemon.statusMu.Lock()
	defer daemon.statusMu.Unlock()
	update(&daemon.status)
}

func (daemon *Daemon) currentStatus() LocalStatus {
	daemon.statusMu.RLock()
	defer daemon.statusMu.RUnlock()
	return daemon.status
}

func writeHelperJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func constantStringEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func nonZeroTimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return timePointer(value)
}
