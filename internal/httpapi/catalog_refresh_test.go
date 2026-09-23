package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/accounts"
	"github.com/Dodelidoo-Labs/open-cdx/internal/catalog"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
	"github.com/Dodelidoo-Labs/open-cdx/internal/routing"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func TestDeviceCatalogUpgradeSurvivesDashboardRefreshAndOlderDevice(t *testing.T) {
	server, store := liveTestServer(t)
	ctx := context.Background()
	account, _, err := store.PutAccount(ctx, storage.AccountInput{
		Credential: storage.OpenAICredential{AccountID: "account", AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)},
		Status:     "ready", CatalogClientVersion: "0.158.0", EntitledModels: []string{"existing-model"},
		RawCatalogSnapshot: []byte(`{"models":[{"slug":"existing-model"}]}`),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := store.CreateEnrollment(ctx, "Updated Codex")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if version := r.URL.Query().Get("client_version"); version != "0.159.0" || r.Header.Get("version") != version {
			t.Errorf("wrong version: %q / %q", version, r.Header.Get("version"))
		}
		fmt.Fprint(w, `{"models":[{"slug":"existing-model"},{"slug":"new-model"}]}`)
	}))
	defer upstream.Close()
	server.accounts = accounts.NewManager(store, openai.New(upstream.Client(), upstream.URL, "client", upstream.URL, upstream.URL))
	server.catalog = catalog.NewManager(store)
	server.status = routing.NewStatusRegistry()
	requestCatalog := func(version, etag string, refresh bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/catalog?codex_version="+version, nil)
		r.Header.Set("If-None-Match", etag)
		r = r.WithContext(context.WithValue(ctx, deviceContextKey{}, storage.Device{ID: enrollment.DeviceID}))
		w := httptest.NewRecorder()
		server.catalogResponse(w, r, refresh)
		return w
	}
	response := requestCatalog("0.159.0", "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"new-model"`) || calls.Load() != 1 {
		t.Fatalf("upgrade sync: status=%d body=%s calls=%d", response.Code, response.Body.String(), calls.Load())
	}
	etag := response.Header().Get("ETag")
	if response = requestCatalog("0.159.0", etag, false); response.Code != http.StatusNotModified || calls.Load() != 1 {
		t.Fatalf("unchanged sync: status=%d calls=%d", response.Code, calls.Load())
	}
	w := httptest.NewRecorder()
	server.adminRefreshCatalog(w, httptest.NewRequest(http.MethodPost, "/admin/catalog/refresh", nil))
	if w.Code != http.StatusSeeOther || calls.Load() != 2 {
		t.Fatalf("dashboard refresh: status=%d calls=%d", w.Code, calls.Load())
	}
	response = requestCatalog("0.150.0", etag, true)
	if response.Code != http.StatusNotModified || calls.Load() != 3 {
		t.Fatalf("older device refresh: status=%d body=%s calls=%d", response.Code, response.Body.String(), calls.Load())
	}
	selector := routing.NewSelector(store, []byte("affinity-secret"))
	selection, err := selector.SelectNative(ctx, enrollment.DeviceID, "new-model", "", "", nil)
	if err != nil || selection.Account.ID != account.ID {
		t.Fatalf("visible model lost its route: %+v, %v", selection, err)
	}
}
