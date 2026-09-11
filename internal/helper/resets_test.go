package helper

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
)

func TestResetControlRefreshesHUDAndHidesStaleTickets(t *testing.T) {
	for _, failStatus := range []bool{false, true} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer device-token" {
				t.Error("missing device authentication")
			}
			switch r.URL.Path {
			case "/api/v1/accounts/account-b/resets/consume":
				_, _ = w.Write([]byte(`{"outcome":"reset","quotas_refreshed":true}`))
			case "/api/v1/device/status":
				if failStatus {
					w.WriteHeader(503)
					return
				}
				_, _ = w.Write([]byte(`{"accounts":[{"id":"account-b","reset_credits":1,"reset_tickets":[{"id":"remaining"}]}],"route":{"state":"connected"}}`))
			default:
				t.Errorf("unexpected path %s", r.URL.Path)
			}
		}))
		daemon := &Daemon{remote: &RemoteClient{BaseURL: upstream.URL, DeviceToken: "device-token", HTTP: upstream.Client()}, localSecret: "local-secret",
			status: LocalStatus{Accounts: []AccountAllowance{{ID: "account-b", ResetCredits: 2, ResetTickets: []openai.ResetTicket{{ID: "one"}, {ID: "two"}}}}}}
		r := httptest.NewRequest("POST", "/control/accounts/account-b/resets/consume", strings.NewReader(`{"idempotency_key":"one","credit_id":"one"}`))
		r.SetPathValue("id", "account-b")
		blocked := httptest.NewRecorder()
		daemon.controlAuth(http.HandlerFunc(daemon.controlConsumeReset)).ServeHTTP(blocked, r)
		if blocked.Code != 403 {
			t.Fatal("local control was not authenticated")
		}
		r.Header.Set("X-OpenCDX-Control", "local-secret")
		w := httptest.NewRecorder()
		daemon.controlAuth(http.HandlerFunc(daemon.controlConsumeReset)).ServeHTTP(w, r)
		upstream.Close()
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		status := daemon.currentStatus()
		want := 1
		if failStatus {
			want = 0
		}
		if status.Accounts[0].ID != "account-b" || len(status.Accounts[0].ResetTickets) != want {
			t.Fatalf("stale HUD state: %+v", status.Accounts)
		}
	}
}
