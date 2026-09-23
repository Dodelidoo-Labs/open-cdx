package accounts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func catalogAccount(t *testing.T, store *storage.Store, version string) storage.Account {
	t.Helper()
	account, _, err := store.PutAccount(context.Background(), storage.AccountInput{
		Credential: storage.OpenAICredential{AccountID: "account-" + version, AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)},
		Status:     "ready", CatalogClientVersion: version,
		EntitledModels: []string{"existing-model"}, RawCatalogSnapshot: []byte(`{"models":[{"slug":"existing-model"}]}`),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func TestCatalogRefreshPreservesVersionAcrossManagersAndOlderDevices(t *testing.T) {
	store := accountTestStore(t)
	account := catalogAccount(t, store, "0.159.0")
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		version := r.URL.Query().Get("client_version")
		if version != "0.159.0" || r.Header.Get("version") != version {
			t.Errorf("discovery version query=%q header=%q", version, r.Header.Get("version"))
			fmt.Fprint(w, `{"models":[{"slug":"existing-model"}]}`)
			return
		}
		fmt.Fprint(w, `{"models":[{"slug":"existing-model"},{"slug":"new-model"}]}`)
	}))
	defer server.Close()
	for _, requested := range []string{"0.159.0", "", "0.0.0", "unknown", "0.99.0"} {
		// Recreate the manager to ensure the version is persisted, not cached
		// only for the lifetime of the process handling the device refresh.
		manager := NewManager(store, openai.New(server.Client(), server.URL, "client", server.URL, server.URL))
		if err := manager.RefreshCatalogs(context.Background(), requested); err != nil {
			t.Fatal(err)
		}
		got, err := store.Account(context.Background(), account.ID, false)
		if err != nil || !reflect.DeepEqual(got.EntitledModels, []string{"existing-model", "new-model"}) {
			t.Fatalf("refresh with %q lost models: %v, %v", requested, got.EntitledModels, err)
		}
	}
	if calls.Load() != 5 {
		t.Fatalf("got %d refreshes, want 5", calls.Load())
	}
}

func TestCatalogSyncUpgradesLegacyVersionAndAllowsRealModelRemoval(t *testing.T) {
	store := accountTestStore(t)
	account := catalogAccount(t, store, "0.0.0")
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("client_version") != "0.159.0" {
			t.Errorf("unexpected discovery version: %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"models":[{"slug":"new-model"}]}`)
	}))
	defer server.Close()
	manager := NewManager(store, openai.New(server.Client(), server.URL, "client", server.URL, server.URL))
	ctx := context.Background()
	// A background poll with no known version preserves the cached account.
	if err := manager.RefreshCatalogs(ctx, ""); err != nil {
		t.Fatal(err)
	}
	got, err := store.Account(ctx, account.ID, false)
	if err != nil || got.Status != "ready" || calls.Load() != 0 || !reflect.DeepEqual(got.EntitledModels, []string{"existing-model"}) {
		t.Fatalf("unknown version changed account: %+v, %v, calls=%d", got, err, calls.Load())
	}
	for _, version := range []string{"0.159.0", "0.159.0", "0.99.0", "0.0.0"} {
		if err := manager.EnsureCatalogVersion(ctx, version); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("routine sync fetched %d times, want only the upgrade", calls.Load())
	}
	got, err = store.Account(ctx, account.ID, false)
	if err != nil || !reflect.DeepEqual(got.EntitledModels, []string{"new-model"}) {
		t.Fatalf("upgrade did not replace entitlements: %v, %v", got.EntitledModels, err)
	}
	version, err := store.AccountCatalogClientVersion(ctx, account.ID)
	if err != nil || version != "0.159.0" {
		t.Fatalf("persisted version=%q, %v", version, err)
	}
}

func TestConcurrentCatalogRefreshCannotOverwriteUpgrade(t *testing.T) {
	store := accountTestStore(t)
	account := catalogAccount(t, store, "0.158.0")
	oldStarted, newStarted, releaseOld := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("client_version") == "0.158.0" {
			close(oldStarted)
			<-releaseOld
			fmt.Fprint(w, `{"models":[{"slug":"existing-model"}]}`)
			return
		}
		close(newStarted)
		fmt.Fprint(w, `{"models":[{"slug":"existing-model"},{"slug":"new-model"}]}`)
	}))
	defer server.Close()
	manager := NewManager(store, openai.New(server.Client(), server.URL, "client", server.URL, server.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- manager.RefreshCatalog(ctx, account.ID, "") }()
	select {
	case <-oldStarted:
	case <-ctx.Done():
		close(releaseOld)
		t.Fatal("first catalog refresh did not start")
	}
	go func() { results <- manager.RefreshCatalog(ctx, account.ID, "0.159.0") }()
	select {
	case <-newStarted:
		// Without serialization, let the newer response commit first to
		// reproduce the late old response overwriting the upgraded catalog.
		if err := <-results; err != nil {
			t.Error(err)
		}
		close(releaseOld)
		<-results
		t.Fatal("catalog fetches overlapped for the same account")
	case <-time.After(100 * time.Millisecond):
		close(releaseOld)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Account(ctx, account.ID, false)
	version, versionErr := store.AccountCatalogClientVersion(ctx, account.ID)
	if err != nil || versionErr != nil || version != "0.159.0" || !reflect.DeepEqual(got.EntitledModels, []string{"existing-model", "new-model"}) {
		t.Fatalf("upgrade lost: models=%v version=%q errors=%v/%v", got.EntitledModels, version, err, versionErr)
	}
}

func TestFailedCatalogUpgradePreservesAvailabilityAndRetries(t *testing.T) {
	store := accountTestStore(t)
	account := catalogAccount(t, store, "0.158.0")
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("client_version") != "0.159.0" {
			t.Errorf("unexpected version: %s", r.URL.RawQuery)
		}
		if calls.Add(1) == 1 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"models":[{"slug":"new-model"}]}`)
	}))
	defer server.Close()
	manager := NewManager(store, openai.New(server.Client(), server.URL, "client", server.URL, server.URL))
	ctx := context.Background()
	if err := manager.EnsureCatalogVersion(ctx, "0.159.0"); err == nil {
		t.Fatal("expected discovery failure")
	}
	got, err := store.Account(ctx, account.ID, false)
	version, versionErr := store.AccountCatalogClientVersion(ctx, account.ID)
	if err != nil || versionErr != nil || version != "0.158.0" || !got.QuotaAvailable(time.Now()) || !reflect.DeepEqual(got.EntitledModels, []string{"existing-model"}) {
		t.Fatalf("failed upgrade changed availability: status=%s models=%v version=%q errors=%v/%v", got.Status, got.EntitledModels, version, err, versionErr)
	}
	if err = manager.EnsureCatalogVersion(ctx, "0.159.0"); err != nil {
		t.Fatal(err)
	}
	version, err = store.AccountCatalogClientVersion(ctx, account.ID)
	if err != nil || version != "0.159.0" || calls.Load() != 2 {
		t.Fatalf("failed upgrade was not retried: version=%q calls=%d err=%v", version, calls.Load(), err)
	}
}
