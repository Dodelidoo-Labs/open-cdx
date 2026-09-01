package openai

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers"
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

func TestQuotaWindowsAreSparseAndOrderedByReportedDuration(t *testing.T) {
	now := time.Date(2026, time.August, 31, 18, 0, 0, 0, time.UTC)
	weeklyReset := now.Add(6*24*time.Hour + 5*time.Hour)
	fiveHourReset := now.Add(2*time.Hour + 13*time.Minute)
	raw := []byte(fmt.Sprintf(`{
		"rate_limit":{
			"primary_window":{"used_percent":36,"limit_window_seconds":18000,"reset_at":%d},
			"secondary_window":{"used_percent":3,"limit_window_seconds":604800,"reset_at":%d}
		}
	}`, fiveHourReset.Unix(), weeklyReset.Unix()))

	windows, err := ParseQuotaWindows(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 {
		t.Fatalf("windows=%d, expected two reported windows: %#v", len(windows), windows)
	}
	if windows[0].Role != "secondary" || windows[0].Label() != "Weekly" || windows[0].RemainingPercent() != 97 {
		t.Fatalf("weekly window was not promoted to the main position: %#v", windows[0])
	}
	if windows[1].Role != "primary" || windows[1].Label() != "5 hours" || windows[1].RemainingPercent() != 64 {
		t.Fatalf("five-hour window was not preserved: %#v", windows[1])
	}
}

func TestQuotaWindowsDoNotInventMissingPlanWindows(t *testing.T) {
	now := time.Date(2026, time.August, 31, 18, 0, 0, 0, time.UTC)
	weeklyReset := now.Add(6 * 24 * time.Hour)
	raw := []byte(fmt.Sprintf(`{
		"plan_type":"pro",
		"rate_limit":{"primary_window":null,"secondary_window":{"used_percent":20,"limit_window_seconds":604800,"reset_at":%d}}
	}`, weeklyReset.Unix()))

	windows, err := ParseQuotaWindows(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 || windows[0].Label() != "Weekly" || windows[0].RemainingPercent() != 80 {
		t.Fatalf("missing five-hour window was fabricated or weekly window was lost: %#v", windows)
	}

	windows, err = ParseQuotaWindows([]byte(`{"plan_type":"plus","rate_limit":null}`), now)
	if err != nil || len(windows) != 0 {
		t.Fatalf("absent windows were fabricated: %#v, %v", windows, err)
	}
}

func TestQuotaWindowRetainsPartialDataButWithholdsPace(t *testing.T) {
	now := time.Date(2026, time.August, 31, 18, 0, 0, 0, time.UTC)
	windows, err := ParseQuotaWindows([]byte(`{
		"rate_limit":{"primary_window":{"used_percent":12,"reset_after_seconds":900}}
	}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 || windows[0].Label() != "Allowance" || !windows[0].ResetAt.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("partial window was not retained: %#v", windows)
	}
	if pace := windows[0].Pace(now); pace.Available {
		t.Fatalf("pace was invented without a reported duration: %#v", pace)
	}

	windows, err = ParseQuotaWindows([]byte(`{
		"rate_limit":{"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_at":0,"reset_after_seconds":900}}
	}`), now)
	if err != nil || len(windows) != 1 || !windows[0].ResetAt.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("reset-after fallback was not used for an empty reset timestamp: %#v, %v", windows, err)
	}

	windows, err = ParseQuotaWindows([]byte(`{
		"rate_limit":{"primary_window":{"limit_window_seconds":18000,"reset_after_seconds":900}}
	}`), now)
	if err != nil || len(windows) != 0 {
		t.Fatalf("window without usage was presented as zero usage: %#v, %v", windows, err)
	}
}

func TestQuotaPaceUsesAllowanceAndTimeRemaining(t *testing.T) {
	now := time.Date(2026, time.August, 31, 18, 0, 0, 0, time.UTC)
	onPace := QuotaWindow{UsedPercent: 36, Duration: 5 * time.Hour, ResetAt: now.Add(2*time.Hour + 13*time.Minute)}.Pace(now)
	if !onPace.Available || onPace.Status != "on_pace" || onPace.RequiredRemainingPercent != 44.3 || onPace.BufferPercent != 19.7 {
		t.Fatalf("unexpected on-pace calculation: %#v", onPace)
	}

	tooFast := QuotaWindow{UsedPercent: 30, Duration: 5 * time.Hour, ResetAt: now.Add(4 * time.Hour)}.Pace(now)
	if !tooFast.Available || tooFast.Status != "too_fast" || tooFast.RequiredRemainingPercent != 80 || tooFast.BufferPercent != -10 {
		t.Fatalf("unexpected too-fast calculation: %#v", tooFast)
	}

	rounded := QuotaWindow{UsedPercent: 21, Duration: 5 * time.Hour, ResetAt: now.Add(4 * time.Hour)}.Pace(now)
	if rounded.Status != "on_pace" || rounded.BufferPercent != -1 {
		t.Fatalf("rounding tolerance was not applied: %#v", rounded)
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
