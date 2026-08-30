package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/accounts"
	"github.com/Dodelidoo-Labs/open-cdx/internal/catalog"
	"github.com/Dodelidoo-Labs/open-cdx/internal/config"
	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
	"github.com/Dodelidoo-Labs/open-cdx/internal/httpapi"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
	"github.com/Dodelidoo-Labs/open-cdx/internal/routing"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
	"github.com/Dodelidoo-Labs/open-cdx/internal/version"
)

func main() {
	if err := run(); err != nil {
		log.Printf("router stopped: %v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.RouterFromFlags(os.Args[1:])
	if err != nil {
		return err
	}
	masterKey, err := config.ReadMasterKey(cfg.MasterKeyFile)
	if err != nil {
		return err
	}
	adminToken, err := config.ReadSecret(cfg.AdminTokenFile, "administrator token")
	if err != nil {
		return err
	}
	box, err := secure.NewBox(masterKey)
	if err != nil {
		return err
	}
	store, err := storage.Open(cfg.DatabasePath, box)
	if err != nil {
		return err
	}
	defer store.Close()
	if err = ensureProviders(store); err != nil {
		return err
	}
	metadataHTTP := &http.Client{Timeout: cfg.HTTPTimeout}
	streamHTTP := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment, MaxIdleConns: 100, MaxIdleConnsPerHost: 20,
			IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 15 * time.Second,
			ExpectContinueTimeout: time.Second, ResponseHeaderTimeout: cfg.HTTPTimeout,
		},
	}
	openAIClient := openai.New(metadataHTTP, cfg.AuthIssuer, cfg.OAuthClientID, cfg.OpenAIBaseURL, cfg.ChatGPTBaseURL)
	accountManager := accounts.NewManager(store, openAIClient)
	catalogManager := catalog.NewManager(store)
	affinitySecret := sha256.Sum256(append(append([]byte(nil), masterKey...), []byte("opencdx-affinity-v1")...))
	selector := routing.NewSelector(store, affinitySecret[:])
	statusRegistry := routing.NewStatusRegistry()
	proxy := routing.NewProxy(store, accountManager, catalogManager, selector, statusRegistry, streamHTTP, cfg.InsecureDevelopment)
	api, err := httpapi.New(store, accountManager, catalogManager, proxy, statusRegistry, adminToken, cfg.PublicBaseURL, cfg.InsecureDevelopment, metadataHTTP)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr: cfg.ListenAddress, Handler: api, ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout: 0, WriteTimeout: 0, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go refreshLoop(ctx, api, accountManager, cfg.CatalogRefreshInterval, cfg.QuotaRefreshInterval)
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("OpenCDX router %s (%s) listening on %s", version.Version, version.Commit, cfg.ListenAddress)
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	case serverErr := <-serverErrors:
		if errors.Is(serverErr, http.ErrServerClosed) {
			return nil
		}
		return serverErr
	}
}

func ensureProviders(store *storage.Store) error {
	ctx := context.Background()
	for _, provider := range []storage.ProviderConfig{
		{Name: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Enabled: false, Health: "unconfigured"},
		{Name: "ollama", BaseURL: "http://127.0.0.1:11434", Enabled: false, Health: "unconfigured"},
	} {
		if _, err := store.Provider(ctx, provider.Name, false); errors.Is(err, storage.ErrNotFound) {
			if err = store.PutProvider(ctx, provider); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

func refreshLoop(ctx context.Context, api *httpapi.Server, accountManager *accounts.Manager, catalogInterval, quotaInterval time.Duration) {
	const backgroundClientVersion = "0.0.0"
	catalogTicker := time.NewTicker(catalogInterval)
	quotaTicker := time.NewTicker(quotaInterval)
	defer catalogTicker.Stop()
	defer quotaTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-catalogTicker.C:
			refreshContext, cancel := context.WithTimeout(ctx, 4*time.Minute)
			_ = api.RefreshProviders(refreshContext)
			_ = accountManager.RefreshAll(refreshContext, backgroundClientVersion)
			cancel()
		case <-quotaTicker.C:
			refreshContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
			_ = accountManager.RefreshAll(refreshContext, backgroundClientVersion)
			cancel()
		}
	}
}
