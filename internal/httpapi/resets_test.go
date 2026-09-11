package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/accounts"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func TestResetRoutesRequireAccountAuthCSRFAndIdempotency(t *testing.T) {
	server, store := liveTestServer(t)
	ctx := context.Background()
	account, _, err := store.PutAccount(ctx, storage.AccountInput{
		Credential: storage.OpenAICredential{AccountID: "test-account", AccessToken: "test-token", RefreshToken: "test-refresh", ExpiresAt: time.Now().Add(time.Hour)},
		Status:     "ready", Plan: "plus", MaskedEmail: "a@example.com", ResetCredits: 1,
		RawQuota: json.RawMessage(`{"rate_limit_reset_credits":{"available_count":1}}`),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	var consumes atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/consume") {
			consumes.Add(1)
			_, _ = w.Write([]byte(`{"code":"reset"}`))
		} else {
			_, _ = w.Write([]byte(`{"rate_limit_reset_credits":{"available_count":0}}`))
		}
	}))
	defer upstream.Close()
	server.accounts = accounts.NewManager(store, openai.New(upstream.Client(), "", "", "", upstream.URL))
	server.sessions[tokenHash("session")] = adminSession{CSRF: "csrf", ExpiresAt: time.Now().Add(time.Hour)}
	enrollment, err := store.CreateEnrollment(ctx, "Reset Mac")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApproveDevice(ctx, enrollment.DeviceID); err != nil {
		t.Fatal(err)
	}
	approved, err := store.EnrollmentStatus(ctx, enrollment.DeviceID, enrollment.EnrollmentSecret)
	if err != nil {
		t.Fatal(err)
	}
	routes := server.routes()
	for _, test := range []struct {
		name, path, body, auth, csrf string
		status                       int
		admin                        bool
	}{
		{name: "unauthenticated device", path: "/api/v1/accounts/" + account.ID + "/resets/consume", body: `{"idempotency_key":"one"}`, status: 401},
		{name: "admin without CSRF", path: "/admin/accounts/" + account.ID + "/resets/consume", body: `{"idempotency_key":"one"}`, admin: true, status: 403},
		{name: "missing key", path: "/api/v1/accounts/" + account.ID + "/resets/consume", body: `{}`, auth: approved.DeviceToken, status: 400},
		{name: "missing account", path: "/api/v1/accounts/missing/resets/consume", body: `{"idempotency_key":"one"}`, auth: approved.DeviceToken, status: 404},
		{name: "device", path: "/api/v1/accounts/" + account.ID + "/resets/consume", body: `{"idempotency_key":"one"}`, auth: approved.DeviceToken, status: 200},
		{name: "admin", path: "/admin/accounts/" + account.ID + "/resets/consume", body: `{"idempotency_key":"two"}`, admin: true, csrf: "csrf", status: 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := consumes.Load()
			r := httptest.NewRequest("POST", test.path, strings.NewReader(test.body))
			r.Header.Set("Content-Type", "application/json")
			if test.auth != "" {
				r.Header.Set("Authorization", "Bearer "+test.auth)
			}
			if test.admin {
				r.AddCookie(&http.Cookie{Name: "opencdx_admin", Value: "session"})
			}
			r.Header.Set("X-CSRF-Token", test.csrf)
			w := httptest.NewRecorder()
			routes.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if test.status != 200 && consumes.Load() != before {
				t.Fatal("rejected request consumed a reset")
			}
			if test.status == 200 && !strings.Contains(w.Body.String(), `"outcome":"reset"`) {
				t.Fatalf("body=%s", w.Body.String())
			}
		})
	}
}
