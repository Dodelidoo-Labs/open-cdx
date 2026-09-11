package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers"
)

func TestResetTicketsAvailabilityAndExpiration(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, raw string
		count     int
	}{
		{"unknown", `{}`, 0},
		{"null", `{"rate_limit_reset_credits":null}`, 0},
		{"empty", `{"rate_limit_reset_credits":{"available_count":0}}`, 0},
		{"count only", `{"rate_limit_reset_credits":{"available_count":3}}`, 3},
		{"capped details", `{"rate_limit_reset_credits":{"available_count":3,"credits":[{"id":"one","status":"available"}]}}`, 3},
		{"expiry boundary", `{"rate_limit_reset_credits":{"available_count":2,"credits":[{"id":"expired","status":"available","expires_at":"2026-09-10T12:00:00Z"},{"id":"valid","status":"available","expires_at":"2026-09-11T00:00:00Z"}]}}`, 1},
		{"authoritative zero", `{"rate_limit_reset_credits":{"available_count":0,"credits":[{"id":"old","status":"available"}]}}`, 0},
		{"bad count", `{"rate_limit_reset_credits":{"available_count":-1}}`, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			tickets := ResetTickets([]byte(test.raw), now)
			if len(tickets) != test.count {
				t.Fatalf("tickets=%+v; want %d", tickets, test.count)
			}
			if test.name == "expiry boundary" && tickets[0].ID != "valid" {
				t.Fatal("wrong credit retained")
			}
		})
	}
}

func TestResetHTTPContract(t *testing.T) {
	for _, test := range []struct{ code, outcome string }{
		{"reset", "reset"}, {"already_redeemed", "alreadyRedeemed"}, {"nothing_to_reset", "nothingToReset"}, {"no_credit", "noCredit"}, {"unknown", ""},
	} {
		t.Run(test.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/wham/rate-limit-reset-credits/consume" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("ChatGPT-Account-ID") != "account-b" {
					t.Error("wrong account auth")
				}
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["redeem_request_id"] != "same-attempt" || body["credit_id"] != "ticket-b" || len(body) != 2 {
					t.Errorf("wrong wire body: %+v", body)
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"code": test.code})
			}))
			defer server.Close()
			client := New(server.Client(), "", "", "", server.URL)
			outcome, err := client.ConsumeReset(context.Background(), providers.Credential{AccessToken: "test-token", AccountID: "account-b"}, ConsumeResetRequest{IdempotencyKey: "same-attempt", CreditID: "ticket-b"})
			if outcome != test.outcome || (err != nil) != (test.outcome == "") {
				t.Fatalf("outcome=%q err=%v", outcome, err)
			}
		})
	}
}

func TestCollectQuotaEnrichesResetDetailsWithoutLosingWindows(t *testing.T) {
	for _, detailResponse := range []string{
		`{"available_count":2,"credits":[{"id":"one","status":"available","expires_at":"2099-09-11T00:00:00Z"}]}`,
		`{}`, `invalid`,
	} {
		t.Run(detailResponse, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/wham/usage" {
					_, _ = w.Write([]byte(`{"plan_type":"plus","rate_limit":{"allowed":true,"primary_window":{"used_percent":75}},"rate_limit_reset_credits":{"available_count":2}}`))
				} else {
					_, _ = w.Write([]byte(detailResponse))
				}
			}))
			defer server.Close()
			quota, err := New(server.Client(), "", "", "", server.URL).CollectQuota(context.Background(), providers.Credential{})
			if err != nil || quota.UsedPercent != 75 || quota.ResetCredits != 2 {
				t.Fatalf("quota=%+v err=%v", quota, err)
			}
			tickets := ResetTickets(quota.Raw, time.Now())
			if len(tickets) != 2 {
				t.Fatalf("tickets=%+v", tickets)
			}
			if strings.Contains(detailResponse, `"credits"`) && tickets[0].ID != "one" {
				t.Fatal("credit details lost")
			}
		})
	}
}
