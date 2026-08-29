package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	secure "github.com/opencdx/opencdx/internal/crypto"
)

func testStore(t *testing.T, path string) *Store {
	t.Helper()
	box, err := secure.NewBox(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(path, box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func accountInput(stableID, access string) AccountInput {
	return AccountInput{
		Credential:  OpenAICredential{AccessToken: access, RefreshToken: "refresh-" + access, IDToken: "id-" + access, AccountID: stableID, ExpiresAt: time.Now().Add(time.Hour)},
		MaskedEmail: "a***z@e***.com", Plan: "plus", Status: "ready", EntitledModels: []string{"gpt-test"},
		RawQuota: []byte(`{"secret_quota":true}`), RawCatalogSnapshot: []byte(`{"models":[{"slug":"gpt-test","unknown":{"preserve":true}}]}`),
	}
}

func TestDuplicateAccountAndEncryptedRestartPersistence(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "router.db")
	store := testStore(t, path)
	created, duplicate, err := store.PutAccount(context.Background(), accountInput("stable-chatgpt-id", "access-secret"), false)
	if err != nil || duplicate {
		t.Fatalf("create account: duplicate=%v err=%v", duplicate, err)
	}
	if _, duplicate, err = store.PutAccount(context.Background(), accountInput("stable-chatgpt-id", "different-token"), false); !duplicate || !errors.Is(err, ErrDuplicateAccount) {
		t.Fatalf("expected duplicate detection, duplicate=%v err=%v", duplicate, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		raw, readErr := os.ReadFile(path + suffix)
		if readErr == nil && (bytes.Contains(raw, []byte("access-secret")) || bytes.Contains(raw, []byte("stable-chatgpt-id")) || bytes.Contains(raw, []byte("secret_quota"))) {
			t.Fatalf("plaintext credential material appeared in SQLite%s", suffix)
		}
	}
	box, _ := secure.NewBox(bytes.Repeat([]byte{0x42}, 32))
	reopened, err := Open(path, box)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	account, err := reopened.Account(context.Background(), created.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if account.Credential.AccessToken != "access-secret" || string(account.RawCatalogSnapshot) == "" || len(account.EntitledModels) != 1 {
		t.Fatal("encrypted account, catalog, or entitlements did not survive restart")
	}
}

func TestExplicitCredentialReplacementPreservesPauseState(t *testing.T) {
	store := testStore(t, ":memory:")
	account, _, err := store.PutAccount(context.Background(), accountInput("stable-replacement-id", "first-access"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetAccountPaused(context.Background(), account.ID, true); err != nil {
		t.Fatal(err)
	}
	replaced, duplicate, err := store.PutAccount(context.Background(), accountInput("stable-replacement-id", "replacement-access"), true)
	if err != nil || !duplicate {
		t.Fatalf("explicit replacement failed: duplicate=%v err=%v", duplicate, err)
	}
	if !replaced.Paused || replaced.Credential.AccessToken != "replacement-access" {
		t.Fatalf("replacement changed routing policy or missed new credential: %#v", replaced)
	}
}

func TestAccountRouteOrderIsPersistentAndValidated(t *testing.T) {
	store := testStore(t, ":memory:")
	first, _, err := store.PutAccount(context.Background(), accountInput("route-first", "first-access"), false)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := store.PutAccount(context.Background(), accountInput("route-second", "second-access"), false)
	if err != nil {
		t.Fatal(err)
	}
	third, _, err := store.PutAccount(context.Background(), accountInput("route-third", "third-access"), false)
	if err != nil {
		t.Fatal(err)
	}

	wanted := []string{third.ID, first.ID, second.ID}
	if err = store.ReorderAccounts(context.Background(), wanted); err != nil {
		t.Fatalf("reorder accounts: %v", err)
	}
	accounts, err := store.Accounts(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for index, account := range accounts {
		if account.ID != wanted[index] || account.RouteOrder != index || account.Primary != (index == 0) {
			t.Fatalf("account %d = %s (route order %d), want %s (%d)", index, account.ID, account.RouteOrder, wanted[index], index)
		}
	}

	if err = store.ReorderAccounts(context.Background(), []string{first.ID, first.ID, second.ID}); !errors.Is(err, ErrInvalidAccountOrder) {
		t.Fatalf("duplicate account order error = %v", err)
	}
	if err = store.ReorderAccounts(context.Background(), []string{first.ID, second.ID}); !errors.Is(err, ErrInvalidAccountOrder) {
		t.Fatalf("partial account order error = %v", err)
	}
}

func TestUsageSupportsAllTimeAndSinceQueries(t *testing.T) {
	store := testStore(t, ":memory:")
	for _, day := range []string{"2024-01-02", "2026-08-28"} {
		if _, err := store.db.ExecContext(context.Background(), `
			INSERT INTO usage_aggregate(day, provider, model_id, account_id, requests, input_tokens, output_tokens)
			VALUES(?,?,?,?,?,?,?)`, day, "openai", "gpt-test", "account", 1, 10, 5); err != nil {
			t.Fatal(err)
		}
	}
	all, err := store.Usage(context.Background(), time.Time{})
	if err != nil || len(all) != 2 {
		t.Fatalf("all-time usage returned %d rows: %v", len(all), err)
	}
	recent, err := store.Usage(context.Background(), time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(recent) != 1 || recent[0].Day != "2026-08-28" {
		t.Fatalf("bounded usage = %#v, err=%v", recent, err)
	}
}

func TestUsageReconciliationReplacesAtomicallyAndPreservesDetailedCounters(t *testing.T) {
	store := testStore(t, ":memory:")
	if _, err := store.db.ExecContext(context.Background(), `
		INSERT INTO usage_aggregate(day, provider, model_id, account_id, requests, input_tokens, output_tokens)
		VALUES('2026-08-27','openai','old-model','account',1,10,5)`); err != nil {
		t.Fatal(err)
	}
	reconciledAt := time.Date(2026, time.August, 28, 20, 0, 0, 0, time.UTC)
	duplicate := []UsageAggregate{
		{Day: "2026-08-28", Provider: "openrouter", ModelID: "openrouter/vendor/model", Requests: 1, InputTokens: 20},
		{Day: "2026-08-28", Provider: "openrouter", ModelID: "openrouter/vendor/model", Requests: 1, InputTokens: 30},
	}
	if err := store.ReplaceUsage(context.Background(), duplicate, UsageReconciliation{ReconciledAt: reconciledAt}); err == nil {
		t.Fatal("duplicate replacement unexpectedly succeeded")
	}
	unchanged, err := store.Usage(context.Background(), time.Time{})
	if err != nil || len(unchanged) != 1 || unchanged[0].ModelID != "old-model" {
		t.Fatalf("failed replacement was not rolled back: %#v, %v", unchanged, err)
	}
	replacement := []UsageAggregate{{
		Day: "2026-08-28", Provider: "openrouter", ModelID: "openrouter/vendor/model", Requests: 2,
		InputTokens: 100, CachedInputTokens: 40, CacheWriteInputTokens: 3,
		OutputTokens: 20, ReasoningOutputTokens: 5,
	}}
	metadata := UsageReconciliation{ReconciledAt: reconciledAt, FilesScanned: 4, EventsImported: 2, RowsImported: 1}
	if err = store.ReplaceUsage(context.Background(), replacement, metadata); err != nil {
		t.Fatal(err)
	}
	usage, err := store.Usage(context.Background(), time.Time{})
	if err != nil || len(usage) != 1 {
		t.Fatalf("replacement usage = %#v, %v", usage, err)
	}
	if usage[0].ModelID != "openrouter/vendor/model" || usage[0].AccountID != "reconciled-history" ||
		usage[0].CachedInputTokens != 40 || usage[0].CacheWriteInputTokens != 3 || usage[0].ReasoningOutputTokens != 5 {
		t.Fatalf("detailed replacement counters were lost: %#v", usage[0])
	}
	storedMetadata, err := store.UsageReconciliation(context.Background())
	if err != nil || !storedMetadata.ReconciledAt.Equal(reconciledAt) || storedMetadata.EventsImported != 2 {
		t.Fatalf("reconciliation metadata = %#v, %v", storedMetadata, err)
	}
}

func TestExistingUsageTableMigratesDetailedCounters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`CREATE TABLE usage_aggregate (
		day TEXT NOT NULL, provider TEXT NOT NULL, model_id TEXT NOT NULL,
		account_id TEXT NOT NULL DEFAULT '', requests INTEGER NOT NULL DEFAULT 0,
		input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (day, provider, model_id, account_id))`)
	if closeErr := database.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	store := testStore(t, path)
	replacement := []UsageAggregate{{
		Day: "2026-08-28", Provider: "openai", ModelID: "gpt-test", Requests: 1,
		InputTokens: 10, CachedInputTokens: 4, CacheWriteInputTokens: 2,
		OutputTokens: 3, ReasoningOutputTokens: 1,
	}}
	if err = store.ReplaceUsage(context.Background(), replacement, UsageReconciliation{ReconciledAt: time.Now(), FilesScanned: 1, EventsImported: 1, RowsImported: 1}); err != nil {
		t.Fatalf("legacy database migration did not add detailed counters: %v", err)
	}
}

func TestExistingAccountsMigrateToDeterministicRouteOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-accounts.db")
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`CREATE TABLE accounts (
		id TEXT PRIMARY KEY, stable_hash BLOB NOT NULL UNIQUE, credential_blob BLOB NOT NULL,
		masked_email TEXT NOT NULL, plan TEXT NOT NULL, status TEXT NOT NULL,
		paused INTEGER NOT NULL DEFAULT 0, primary_account INTEGER NOT NULL DEFAULT 0,
		quota_used_percent REAL NOT NULL DEFAULT 0, quota_reset_at INTEGER NOT NULL DEFAULT 0,
		reset_credits INTEGER NOT NULL DEFAULT 0, raw_quota_blob BLOB,
		last_error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	)`)
	if err == nil {
		_, err = database.Exec(`INSERT INTO accounts
			(id, stable_hash, credential_blob, masked_email, plan, status, created_at, updated_at)
			VALUES ('later', X'02', X'02', 'later', 'plus', 'ready', 20, 20),
			       ('earlier-b', X'03', X'03', 'earlier-b', 'plus', 'ready', 10, 10),
			       ('earlier-a', X'01', X'01', 'earlier-a', 'plus', 'ready', 10, 10)`)
	}
	if closeErr := database.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}

	store := testStore(t, path)
	rows, err := store.db.QueryContext(context.Background(), `SELECT id, route_order FROM accounts ORDER BY route_order`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wanted := []string{"earlier-a", "earlier-b", "later"}
	var index int
	for rows.Next() {
		var id string
		var routeOrder int
		if err = rows.Scan(&id, &routeOrder); err != nil {
			t.Fatal(err)
		}
		if index >= len(wanted) || id != wanted[index] || routeOrder != index {
			t.Fatalf("migrated account %d = %s (route order %d)", index, id, routeOrder)
		}
		index++
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(wanted) {
		t.Fatalf("migrated %d accounts, want %d", index, len(wanted))
	}
}

func TestCredentialRemovalDeletesProviderCatalogMaterial(t *testing.T) {
	store := testStore(t, ":memory:")
	account, _, err := store.PutAccount(context.Background(), accountInput("stable-delete-id", "delete-access"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	if snapshots, snapshotErr := store.CatalogSnapshots(context.Background(), "openai"); snapshotErr != nil || len(snapshots) != 0 {
		t.Fatalf("account catalog survived credential removal: snapshots=%d err=%v", len(snapshots), snapshotErr)
	}
	if err = store.PutProvider(context.Background(), ProviderConfig{Name: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Enabled: true, APIKey: "secret", Health: "healthy"}); err != nil {
		t.Fatal(err)
	}
	if present, secretErr := store.ProviderHasSecret(context.Background(), "openrouter"); secretErr != nil || !present {
		t.Fatalf("stored provider credential was not detected: present=%v err=%v", present, secretErr)
	}
	if err = store.PutCatalogSnapshot(context.Background(), CatalogSnapshot{Provider: "openrouter", Raw: []byte(`{"data":[]}`), FetchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err = store.ClearProviderSecret(context.Background(), "openrouter"); err != nil {
		t.Fatal(err)
	}
	if present, secretErr := store.ProviderHasSecret(context.Background(), "openrouter"); secretErr != nil || present {
		t.Fatalf("removed provider credential still appears present: present=%v err=%v", present, secretErr)
	}
	if snapshots, snapshotErr := store.CatalogSnapshots(context.Background(), "openrouter"); snapshotErr != nil || len(snapshots) != 0 {
		t.Fatalf("provider catalog survived credential removal: snapshots=%d err=%v", len(snapshots), snapshotErr)
	}
}

func TestOAuthStateExpiryAndOneTimeUse(t *testing.T) {
	store := testStore(t, ":memory:")
	enrollment, err := store.CreateEnrollment(context.Background(), "OAuth Mac")
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := store.CreateOAuthTransaction(context.Background(), enrollment.DeviceID, "correct-state", "pkce-verifier", "http://localhost:1455/auth/callback", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ConsumeOAuthTransaction(context.Background(), transaction.ID, enrollment.DeviceID, "wrong-state", time.Now()); !errors.Is(err, ErrOAuthInvalid) {
		t.Fatalf("wrong state was not rejected: %v", err)
	}
	consumed, err := store.ConsumeOAuthTransaction(context.Background(), transaction.ID, enrollment.DeviceID, "correct-state", time.Now())
	if err != nil || consumed.Verifier != "pkce-verifier" {
		t.Fatalf("valid transaction failed: %#v %v", consumed, err)
	}
	if _, err = store.ConsumeOAuthTransaction(context.Background(), transaction.ID, enrollment.DeviceID, "correct-state", time.Now()); !errors.Is(err, ErrOAuthInvalid) {
		t.Fatalf("replayed transaction was not rejected: %v", err)
	}
}

func TestDeviceApprovalAcknowledgementAndRevocation(t *testing.T) {
	store := testStore(t, ":memory:")
	enrollment, err := store.CreateEnrollment(context.Background(), "Second Mac")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.EnrollmentStatus(context.Background(), enrollment.DeviceID, enrollment.EnrollmentSecret); !errors.Is(err, ErrEnrollmentPending) {
		t.Fatalf("expected pending enrollment: %v", err)
	}
	if err = store.ApproveDevice(context.Background(), enrollment.DeviceID); err != nil {
		t.Fatal(err)
	}
	issued, err := store.EnrollmentStatus(context.Background(), enrollment.DeviceID, enrollment.EnrollmentSecret)
	if err != nil || issued.DeviceToken == "" {
		t.Fatalf("approved credential was not issued: %v", err)
	}
	if _, err = store.AuthenticateDevice(context.Background(), issued.DeviceToken); err != nil {
		t.Fatal(err)
	}
	if err = store.AcknowledgeEnrollment(context.Background(), enrollment.DeviceID, enrollment.EnrollmentSecret); err != nil {
		t.Fatal(err)
	}
	if _, err = store.EnrollmentStatus(context.Background(), enrollment.DeviceID, enrollment.EnrollmentSecret); !errors.Is(err, ErrEnrollmentComplete) {
		t.Fatalf("one-time credential remained readable: %v", err)
	}
	if err = store.RevokeDevice(context.Background(), enrollment.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AuthenticateDevice(context.Background(), issued.DeviceToken); err == nil {
		t.Fatal("revoked device credential still authenticated")
	}
}
