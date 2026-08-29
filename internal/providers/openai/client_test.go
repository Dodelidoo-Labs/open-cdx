package openai

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/opencdx/opencdx/internal/providers"
)

func TestAuthorizationURLUsesPKCEAndExplicitLogin(t *testing.T) {
	client := New(nil, "https://auth.openai.com", "client-id", "https://chatgpt.com/backend-api/codex", "https://chatgpt.com/backend-api")
	generated, err := client.AuthorizationURL("http://localhost:1455/auth/callback", "state-value", "challenge-value")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(generated)
	query := parsed.Query()
	for name, expected := range map[string]string{
		"response_type": "code", "code_challenge": "challenge-value", "code_challenge_method": "S256",
		"state": "state-value", "prompt": "login", "max_age": "0", "codex_cli_simplified_flow": "true",
	} {
		if query.Get(name) != expected {
			t.Fatalf("%s=%q, expected %q", name, query.Get(name), expected)
		}
	}
	if query.Get("scope") != authorizeScope {
		t.Fatalf("unexpected scope %q", query.Get("scope"))
	}
	verifier := "example-verifier"
	digest := sha256.Sum256([]byte(verifier))
	if base64.RawURLEncoding.EncodeToString(digest[:]) == query.Get("code_challenge") {
		t.Fatal("test fixture accidentally reused the supplied challenge")
	}
}

func TestNativeCatalogPreservesUnknownFields(t *testing.T) {
	raw := []byte(`{"models":[{"slug":"gpt-native","supported_reasoning_levels":[{"effort":"ultra"}],"safety":{"opaque":[1,2,3]},"new_field":true}]}`)
	models, err := ParseNativeModels(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || string(models[0].Raw) != `{"slug":"gpt-native","supported_reasoning_levels":[{"effort":"ultra"}],"safety":{"opaque":[1,2,3]},"new_field":true}` {
		t.Fatalf("native entry changed: %s", models[0].Raw)
	}
}

func TestLimitReachedQuotaCannotRemainRoutable(t *testing.T) {
	quota, err := ParseQuota([]byte(`{"plan_type":"plus","rate_limit":{"allowed":false,"limit_reached":true,"primary_window":{"used_percent":12,"reset_at":2000000000}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !quota.LimitReached || quota.UsedPercent != 100 {
		t.Fatalf("limit-reached quota remained available: %#v", quota)
	}
}

func TestCurrentSpendControlAndReachedTypeExhaustQuota(t *testing.T) {
	quota, err := ParseQuota([]byte(`{"plan_type":"business","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":25,"reset_at":2000000000}},"spend_control":{"reached":true,"individual_limit":{"used_percent":40,"reset_at":2000000100}},"rate_limit_reached_type":{"type":"workspace_member_usage_limit_reached"},"rate_limit_reset_credits":{"available_count":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !quota.LimitReached || quota.UsedPercent != 100 || quota.ResetCredits != 2 || quota.ResetAt.Unix() != 2_000_000_100 {
		t.Fatalf("current quota fields were not applied: %#v", quota)
	}
}

func TestAdditionalQuotasExposeSparkOnlyWhenReported(t *testing.T) {
	quotas, err := ParseAdditionalQuotas([]byte(`{
		"plan_type":"pro",
		"additional_rate_limits":[
			{"limit_name":"Codex Spark","metered_feature":"codex_spark","rate_limit":{"primary_window":{"used_percent":25,"reset_at":2000000000},"secondary_window":{"used_percent":41,"reset_at":2000000100}}},
			{"limit_name":"Unavailable","metered_feature":"missing_windows","rate_limit":null}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(quotas) != 1 {
		t.Fatalf("unexpected additional quota buckets: %#v", quotas)
	}
	quota := quotas[0]
	if quota.Name != "Codex Spark" || quota.MeteredFeature != "codex_spark" || quota.UsedPercent != 41 || quota.ResetAt.Unix() != 2_000_000_100 {
		t.Fatalf("Spark quota was not parsed conservatively: %#v", quota)
	}

	quotas, err = ParseAdditionalQuotas([]byte(`{"plan_type":"plus"}`))
	if err != nil || len(quotas) != 0 {
		t.Fatalf("an absent additional quota was fabricated: %#v, %v", quotas, err)
	}
}

func TestAccountCheckSupportsListAndKeyedShapes(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"accounts":[{"id":"account-a"}],"account_ordering":["account-a"]}`),
		[]byte(`{"accounts":{"workspace":{"account":{"account_id":"account-a"}}},"account_ordering":["workspace"]}`),
	} {
		matched, err := accountCheckContains(raw, "account-a")
		if err != nil || !matched {
			t.Fatalf("account-check shape was not recognized: matched=%v err=%v", matched, err)
		}
	}
	matched, err := accountCheckContains([]byte(`{"accounts":[{"id":"different"}]}`), "account-a")
	if err != nil || matched {
		t.Fatalf("mismatched account was accepted: matched=%v err=%v", matched, err)
	}
}

func TestRefreshWithoutNewIDTokenRetainsValidatedIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"access_token": "new-access", "expires_in": 3600})
	}))
	defer server.Close()
	client := New(server.Client(), server.URL, "client", server.URL, server.URL)
	credential := providers.Credential{
		AccessToken: "old-access", RefreshToken: "old-refresh", IDToken: openAITestJWT("account-a", time.Now().Add(-time.Hour)),
		AccountID: "account-a", UserID: "user-a", ExpiresAt: time.Now().Add(-time.Minute),
	}
	refreshed, err := client.Refresh(context.Background(), credential)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "new-access" || refreshed.RefreshToken != "old-refresh" || refreshed.AccountID != "account-a" || refreshed.UserID != "user-a" || !refreshed.ExpiresAt.After(time.Now().Add(50*time.Minute)) {
		t.Fatalf("refresh did not preserve validated identity: %#v", refreshed)
	}
}

func openAITestJWT(accountID string, expires time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(map[string]any{"exp": expires.Unix(), "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID, "chatgpt_user_id": "user-a"}})
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}
