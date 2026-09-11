package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/catalog"
	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func TestCatalogConflictDetailsUseCurrentSnapshotsAndRequireAdmin(t *testing.T) {
	box, _ := secure.NewBox(bytes.Repeat([]byte{0x35}, 32))
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	definitions := []string{
		`{"models":[{"slug":"model-a","catalog_id":"pro-123","limit":100,"shared":"UNCHANGED-CATALOG-CONTENT"},{"slug":"model-b","effort":"high"}]}`,
		`{"models":[{"slug":"model-a","catalog_id":"plus-456","limit":200,"shared":"UNCHANGED-CATALOG-CONTENT"},{"slug":"model-b","effort":"low"}]}`,
	}
	var accounts []storage.Account
	for i, plan := range []string{"pro", "plus"} {
		account, _, err := store.PutAccount(ctx, storage.AccountInput{
			Credential:  storage.OpenAICredential{AccountID: "private-stable-" + plan, AccessToken: "private-access-" + plan, RefreshToken: "private-refresh-" + plan},
			MaskedEmail: plan + "***@example.com", Plan: plan, Status: "ready", EntitledModels: []string{"model-a", "model-b"}, RawCatalogSnapshot: json.RawMessage(definitions[i]),
		}, false)
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, account)
	}
	// An old generic warning must neither limit current detail nor invent stale models.
	if err = store.ReplaceConflicts(ctx, map[string]string{"old-model": "generic legacy warning"}); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store, location: time.UTC, sessions: map[string]adminSession{tokenHash("session"): {CSRF: "csrf", ExpiresAt: time.Now().Add(time.Hour)}}}
	handler := server.routes()
	send := func(model string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/admin/catalog/conflicts?model="+model, nil)
		if authenticated {
			r.AddCookie(&http.Cookie{Name: "opencdx_admin", Value: "session"})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := send("model-a", false); w.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated catalog disclosure: %d", w.Code)
	}
	page, err := server.dashboardData(ctx, "csrf")
	if err != nil || len(page.Conflicts) != 2 || page.Conflicts[0].Model != "model-a" || page.Conflicts[1].Model != "model-b" {
		t.Fatalf("dashboard hid current models: %#v %v", page.Conflicts, err)
	}
	if !strings.Contains(page.Conflicts[0].Detail, "2 differing fields") {
		t.Fatalf("incorrect discrepancy summary: %#v", page.Conflicts)
	}
	response := send("model-a", true)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("detail failed: %d %s", response.Code, response.Body.String())
	}
	var detail catalog.NativeConflict
	if err = json.Unmarshal(response.Body.Bytes(), &detail); err != nil || len(detail.Fields) != 2 || len(detail.Sources) != 2 {
		t.Fatalf("incomplete details: %#v %v", detail, err)
	}
	for _, excluded := range []string{"private-stable", "private-access", "private-refresh", "UNCHANGED-CATALOG-CONTENT", "model-b", "shared"} {
		if strings.Contains(response.Body.String(), excluded) {
			t.Fatalf("detail disclosed %q", excluded)
		}
	}
	if !detail.Sources[0].Retained || detail.Sources[0].Plan != "pro" || detail.Fields[0].Values[1].JSON != `"plus-456"` {
		t.Fatalf("incorrect values/labels: %#v", detail)
	}
	if err = store.PutCatalogSnapshot(ctx, storage.CatalogSnapshot{Provider: "openai", AccountID: accounts[1].ID, Raw: json.RawMessage(definitions[0]), FetchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if w := send("model-a", true); w.Code != 404 || !strings.Contains(w.Body.String(), "no longer has a conflict") {
		t.Fatalf("resolved conflict remained: %d %s", w.Code, w.Body.String())
	}
	page, err = server.dashboardData(ctx, "csrf")
	if err != nil || len(page.Conflicts) != 0 {
		t.Fatalf("stale warning remained: %#v %v", page.Conflicts, err)
	}
	if _, err = store.Database().ExecContext(ctx, `UPDATE catalog_snapshots SET raw_json=? WHERE provider='openai' AND account_id=?`, []byte("invalid"), accounts[1].ID); err != nil {
		t.Fatal(err)
	}
	page, err = server.dashboardData(ctx, "csrf")
	if err != nil || page.ConflictError == "" {
		t.Fatalf("comparison failure silently hidden: %#v %v", page, err)
	}
}
