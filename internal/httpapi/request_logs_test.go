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

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func TestRequestLogAdminBackupRoundTrip(t *testing.T) {
	box, _ := secure.NewBox(bytes.Repeat([]byte{42}, 32))
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := &Server{store: store, sessions: map[string]adminSession{tokenHash("session"): {CSRF: "csrf", ExpiresAt: time.Now().Add(time.Hour)}}}
	handler := server.routes()
	send := func(method, path, body, csrf string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		if authenticated {
			r.AddCookie(&http.Cookie{Name: "opencdx_admin", Value: "session"})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/admin/logs", "/admin/logs/export", "/admin/logs/id"} {
		if w := send("GET", path, "", "", false); w.Code != 303 {
			t.Fatalf("unauthenticated access to %s: %d", path, w.Code)
		}
	}
	entry := storage.RequestLog{ID: "roundtrip", StartedAt: time.Now().UTC(), Outcome: "error", Status: 502, ErrorMessage: "provider failure"}
	raw, _ := json.Marshal([]requestLogBackup{{Version: 1, Log: entry}})
	if w := send("POST", "/admin/logs/import", string(raw), "wrong", true); w.Code != 403 {
		t.Fatalf("missing CSRF protection: %d", w.Code)
	}
	if w := send("POST", "/admin/logs/import", string(raw), "csrf", true); w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	export := send("GET", "/admin/logs/export", "", "", true)
	var backup requestLogBackup
	if export.Code != 200 || json.Unmarshal(export.Body.Bytes(), &backup) != nil || backup.Log.ID != entry.ID || !strings.Contains(export.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("export: %d %s", export.Code, export.Body.String())
	}
	// Restore after a database wipe, then repeat safely.
	if _, err = store.Database().Exec("DELETE FROM request_logs"); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal([]requestLogBackup{backup})
	for i := 0; i < 2; i++ {
		if w := send("POST", "/admin/logs/import", string(raw), "csrf", true); w.Code != 200 {
			t.Fatalf("restore %d: %s", i, w.Body.String())
		}
	}
	page, err := store.RequestLogs(context.Background(), storage.RequestLogFilter{})
	if err != nil || len(page.Logs) != 1 || page.Logs[0].ErrorMessage != "provider failure" {
		t.Fatalf("roundtrip: %#v %v", page, err)
	}
	w := send("GET", "/admin/logs/roundtrip", "", "", true)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("detail: %d", w.Code)
	}
	// Arbitrary prompt/output fields are never accepted into the metadata backup format.
	invalid := strings.Replace(string(raw), `"log":{`, `"log":{"input":"private",`, 1)
	if w := send("POST", "/admin/logs/import", invalid, "csrf", true); w.Code != 400 {
		t.Fatal("unknown content fields accepted")
	}
	if w := send("POST", "/admin/logs/import", string(raw)+`{}`, "csrf", true); w.Code != 400 {
		t.Fatal("trailing input accepted")
	}
}
