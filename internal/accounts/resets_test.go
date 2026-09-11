package accounts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func TestConsumeResetAccountScopeRetryAndRefreshFailure(t *testing.T) {
	for _, refreshFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "refreshed", true: "refresh failed"}[refreshFails], func(t *testing.T) {
			ctx := context.Background()
			store := accountTestStore(t)
			create := func(id string) storage.Account {
				account, _, err := store.PutAccount(ctx, storage.AccountInput{
					Credential:  storage.OpenAICredential{AccountID: id, AccessToken: "token-" + id, RefreshToken: "test-refresh", ExpiresAt: time.Now().Add(time.Hour)},
					MaskedEmail: id + "@example.com", Plan: "plus", Status: "ready", ResetCredits: 2,
					QuotaUsedPercent: 100, RawQuota: json.RawMessage(`{"rate_limit_reset_credits":{"available_count":2}}`),
				}, false)
				if err != nil {
					t.Fatal(err)
				}
				return account
			}
			a, b := create("account-a"), create("account-b")
			consumed := 0
			seen := map[string]bool{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("ChatGPT-Account-ID") != "account-b" {
					t.Error("reset touched wrong account")
				}
				switch r.URL.Path {
				case "/wham/rate-limit-reset-credits/consume":
					var input map[string]string
					_ = json.NewDecoder(r.Body).Decode(&input)
					code := "already_redeemed"
					if !seen[input["redeem_request_id"]] {
						code = "reset"
						consumed++
						seen[input["redeem_request_id"]] = true
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
				case "/wham/usage":
					if refreshFails {
						w.WriteHeader(503)
						return
					}
					_, _ = w.Write([]byte(`{"plan_type":"plus","rate_limit":{"allowed":true,"primary_window":{"used_percent":5}},"rate_limit_reset_credits":{"available_count":1}}`))
				case "/wham/rate-limit-reset-credits":
					_, _ = w.Write([]byte(`{"available_count":1,"credits":[{"id":"remaining","status":"available"}]}`))
				default:
					t.Errorf("unexpected upstream path %s", r.URL.Path)
				}
			}))
			defer upstream.Close()
			manager := NewManager(store, openai.New(upstream.Client(), "", "", "", upstream.URL))
			for _, expected := range []string{"reset", "alreadyRedeemed"} {
				result, err := manager.ConsumeReset(ctx, b.ID, openai.ConsumeResetRequest{IdempotencyKey: "same-attempt", CreditID: "one"})
				if err != nil || result.Outcome != expected || result.QuotasRefreshed == refreshFails {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			}
			if consumed != 1 {
				t.Fatalf("consumed %d credits", consumed)
			}
			untouched, _ := store.Account(ctx, a.ID, false)
			if untouched.ResetCredits != 2 || untouched.QuotaUsedPercent != 100 {
				t.Fatal("other account changed")
			}
			updated, _ := store.Account(ctx, b.ID, false)
			wantCount := 1
			if refreshFails {
				wantCount = 0
			}
			if len(openai.ResetTickets(updated.RawQuota, time.Now())) != wantCount {
				t.Fatalf("stale reset metadata: %s", updated.RawQuota)
			}
			if !refreshFails && updated.QuotaUsedPercent != 5 {
				t.Fatal("usage windows were not refreshed")
			}
		})
	}
}
