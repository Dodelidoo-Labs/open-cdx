package helper

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestModelsFollowCatalogChanges(t *testing.T) {
	d := &Daemon{catalogPath: filepath.Join(t.TempDir(), "catalog.json"), localSecret: strings.Repeat("s", 32)}
	token, err := IssueLocalToken(d.localSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	handler := d.localAuth(http.HandlerFunc(d.models))
	get := func(etag, bearer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := get("", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status: %d", w.Code)
	}
	if w := get("", token); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing catalog status: %d", w.Code)
	}
	write := func(raw string) {
		t.Helper()
		if err := AtomicWrite(d.catalogPath, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"models":[{"slug":"openrouter/test/model","visibility":"list","context_window":128000,"supported_reasoning_levels":[{"effort":"low","description":"Quick"},{"effort":"high","description":"Deep"}],"default_reasoning_level":"low","base_instructions":"PRIVATE"},{"slug":"hidden","visibility":"hide"}]}`)
	first := get("", token)
	if first.Code != 200 || strings.Contains(first.Body.String(), "PRIVATE") || strings.Contains(first.Body.String(), "hidden") {
		t.Fatalf("discovery: %d %s", first.Code, first.Body)
	}
	if w := get(first.Header().Get("ETag"), token); w.Code != 304 {
		t.Fatalf("unchanged status: %d", w.Code)
	}
	// Same ID, different metadata must invalidate the representation.
	write(`{"models":[{"slug":"openrouter/test/model","visibility":"list","context_window":256000,"supported_reasoning_levels":[{"effort":"medium"}],"default_reasoning_level":"medium"}]}`)
	changed := get(first.Header().Get("ETag"), token)
	if changed.Code != 200 || changed.Header().Get("ETag") == first.Header().Get("ETag") || !strings.Contains(changed.Body.String(), `"supported_efforts":["medium"]`) || !strings.Contains(changed.Body.String(), `"context_length":256000`) {
		t.Fatalf("metadata update not visible: %s", changed.Body)
	}
	// Replace membership, then remove the last model. No old IDs may survive.
	write(`{"models":[{"slug":"new","visibility":"list","supported_reasoning_levels":[{"effort":"none"}]}]}`)
	replaced := get("", token)
	if strings.Contains(replaced.Body.String(), "openrouter/test/model") || !strings.Contains(replaced.Body.String(), `"id":"new"`) {
		t.Fatalf("membership update: %s", replaced.Body)
	}
	write(`{"models":[]}`)
	empty := get("", token)
	var payload struct {
		Data []any `json:"data"`
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &payload); err != nil || empty.Code != 200 || payload.Data == nil || len(payload.Data) != 0 {
		t.Fatalf("empty list: %d %s", empty.Code, empty.Body)
	}
	write(`{"models":`)
	if w := get("", token); w.Code != 503 {
		t.Fatalf("invalid catalog status: %d", w.Code)
	}
}
