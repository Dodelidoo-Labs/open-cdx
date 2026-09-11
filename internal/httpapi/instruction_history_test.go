package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func TestInstructionHistoryAPIIsPrivatePagedAndLoadsOnlyRequestedContent(t *testing.T) {
	box, _ := secure.NewBox(bytes.Repeat([]byte{0x67}, 32))
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	account, _, err := store.PutAccount(ctx, storage.AccountInput{
		Credential: storage.OpenAICredential{AccountID: "private-stable", AccessToken: "private-access", RefreshToken: "private-refresh"}, MaskedEmail: "p***@example.com", Plan: "pro", Status: "ready", EntitledModels: []string{"model"},
		RawCatalogSnapshot: json.RawMessage(`{"models":[{"slug":"model","base_instructions":"original instruction","description":"UNRELATED-CATALOG"}]}`), CatalogClientVersion: "1.0.0",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 52; i++ {
		if err = store.UpdateAccountCatalog(ctx, account.ID, json.RawMessage(fmt.Sprintf(`{"models":[{"slug":"model","base_instructions":"instruction version %d","description":"UNRELATED-CATALOG"}]}`, i)), []string{"model"}, "2.0.0"); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{store: store, sessions: map[string]adminSession{tokenHash("session"): {CSRF: "csrf", ExpiresAt: time.Now().Add(time.Hour)}}}
	handler := server.routes()
	send := func(path string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if authenticated {
			r.AddCookie(&http.Cookie{Name: "opencdx_admin", Value: "session"})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/admin/instructions", "/admin/instructions/status", "/admin/instructions/1?path=%2Fbase_instructions"} {
		if w := send(path, false); w.Code != 303 {
			t.Fatalf("instruction data not protected: %s", path)
		}
	}
	response := send("/admin/instructions?kind=changed", true)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("history: %d %s", response.Code, response.Body.String())
	}
	var page storage.InstructionHistoryPage
	if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Revisions) != 50 || page.NextBefore == 0 {
		t.Fatalf("pagination: %#v %v", page, err)
	}
	for _, text := range []string{"instruction version", "UNRELATED-CATALOG", "private-stable", "private-access", "private-refresh"} {
		if strings.Contains(response.Body.String(), text) {
			t.Fatalf("list exposed %q", text)
		}
	}
	older := send(fmt.Sprintf("/admin/instructions?kind=changed&before=%d", page.NextBefore), true)
	var next storage.InstructionHistoryPage
	_ = json.Unmarshal(older.Body.Bytes(), &next)
	if len(next.Revisions) != 2 {
		t.Fatal("older revisions lost")
	}
	id := page.Revisions[0].ID
	detail := send(fmt.Sprintf("/admin/instructions/%d?path=%%2Fbase_instructions", id), true)
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), "instruction version 50") || !strings.Contains(detail.Body.String(), "instruction version 51") {
		t.Fatalf("before/after missing: %s", detail.Body.String())
	}
	if send(fmt.Sprintf("/admin/instructions/%d?path=%%2Fdescription", id), true).Code != 404 {
		t.Fatal("untracked catalog field accessible through diff endpoint")
	}
	if send("/admin/instructions/999999", true).Code != 404 {
		t.Fatal("missing revision accepted")
	}
	var status storage.InstructionHistoryStatus
	_ = json.Unmarshal(send("/admin/instructions/status", true).Body.Bytes(), &status)
	if status.Unseen != 52 {
		t.Fatalf("changed-only unread count: %#v", status)
	}
	_ = json.Unmarshal(send(fmt.Sprintf("/admin/instructions/status?after=%d", status.LatestChangeID), true).Body.Bytes(), &status)
	if status.Unseen != 0 {
		t.Fatal("seen marker ignored")
	}
}
