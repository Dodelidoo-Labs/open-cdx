package accounts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	secure "github.com/opencdx/opencdx/internal/crypto"
	"github.com/opencdx/opencdx/internal/providers/openai"
	"github.com/opencdx/opencdx/internal/storage"
)

func accountTestStore(t *testing.T) *storage.Store {
	t.Helper()
	box, _ := secure.NewBox(bytes.Repeat([]byte{9}, 32))
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestStartOAuthPersistsVerifierMatchingPKCEChallenge(t *testing.T) {
	store := accountTestStore(t)
	enrollment, err := store.CreateEnrollment(context.Background(), "PKCE Mac")
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store, openai.New(nil, "https://auth.openai.com", "client", "https://chatgpt.com/backend-api/codex", "https://chatgpt.com/backend-api"))
	start, err := manager.StartOAuth(context.Background(), enrollment.DeviceID, "http://localhost:1455/auth/callback")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(start.AuthorizationURL)
	state := parsed.Query().Get("state")
	transaction, err := store.ConsumeOAuthTransaction(context.Background(), start.TransactionID, enrollment.DeviceID, state, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(transaction.Verifier))
	expected := base64.RawURLEncoding.EncodeToString(digest[:])
	if parsed.Query().Get("code_challenge") != expected || transaction.Verifier == "" {
		t.Fatal("authorization challenge did not match the encrypted PKCE verifier")
	}
}

func TestRefreshTokenRotationIsSingleFlight(t *testing.T) {
	var refreshCalls atomic.Int64
	accountID := "stable-account"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/oauth/token" {
			http.NotFound(writer, request)
			return
		}
		call := refreshCalls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": fmt.Sprintf("new-access-%d", call), "refresh_token": "new-refresh", "id_token": testJWT(accountID, time.Now().Add(time.Hour)), "expires_in": 3600,
		})
	}))
	defer server.Close()
	store := accountTestStore(t)
	created, _, err := store.PutAccount(context.Background(), storage.AccountInput{
		Credential:  storage.OpenAICredential{AccessToken: "old-access", RefreshToken: "old-refresh", IDToken: testJWT(accountID, time.Now().Add(time.Hour)), AccountID: accountID, ExpiresAt: time.Now().Add(time.Minute)},
		MaskedEmail: "a***t@e***.com", Plan: "plus", Status: "ready", EntitledModels: []string{"gpt-test"},
		RawCatalogSnapshot: []byte(`{"models":[{"slug":"gpt-test"}]}`),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store, openai.New(server.Client(), server.URL, "client", server.URL, server.URL))
	var wait sync.WaitGroup
	errorsChannel := make(chan error, 20)
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			credential, refreshErr := manager.FreshCredential(context.Background(), created.ID)
			if refreshErr != nil {
				errorsChannel <- refreshErr
				return
			}
			if credential.AccessToken != "new-access-1" {
				errorsChannel <- &unexpectedToken{credential.AccessToken}
			}
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err = range errorsChannel {
		t.Fatal(err)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("refresh endpoint called %d times", refreshCalls.Load())
	}

	// Concurrent upstream 401s all report the same rejected access token. Only
	// the first goroutine should rotate it; the rest observe the new value.
	errorsChannel = make(chan error, 20)
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			credential, refreshErr := manager.ForceRefreshCredential(context.Background(), created.ID, "new-access-1")
			if refreshErr != nil {
				errorsChannel <- refreshErr
				return
			}
			if credential.AccessToken != "new-access-2" {
				errorsChannel <- &unexpectedToken{credential.AccessToken}
			}
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err = range errorsChannel {
		t.Fatal(err)
	}
	if refreshCalls.Load() != 2 {
		t.Fatalf("forced refresh endpoint called %d total times", refreshCalls.Load())
	}
}

type unexpectedToken struct{ value string }

func (err *unexpectedToken) Error() string { return "unexpected refreshed token: " + err.value }

func testJWT(accountID string, expires time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(map[string]any{"exp": expires.Unix(), "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID, "chatgpt_user_id": "user"}})
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}
