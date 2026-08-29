package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	secure "github.com/opencdx/opencdx/internal/crypto"
)

func (store *Store) PutAccount(ctx context.Context, input AccountInput, replace bool) (Account, bool, error) {
	if input.Credential.AccountID == "" || input.Credential.AccessToken == "" || input.Credential.RefreshToken == "" {
		return Account{}, false, errors.New("validated OpenAI identity and tokens are required")
	}
	stableHash := secure.Digest(input.Credential.AccountID)
	var existingID string
	err := store.db.QueryRowContext(ctx, `SELECT id FROM accounts WHERE stable_hash = ?`, stableHash).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Account{}, false, err
	}
	duplicate := err == nil
	if duplicate && !replace {
		return Account{}, true, ErrDuplicateAccount
	}
	accountID := existingID
	if accountID == "" {
		accountID, err = secure.RandomURLSafe(18)
		if err != nil {
			return Account{}, false, err
		}
	}
	credentialJSON, err := json.Marshal(input.Credential)
	if err != nil {
		return Account{}, false, err
	}
	credentialBlob, err := store.box.Seal(credentialJSON, []byte("account:"+accountID))
	if err != nil {
		return Account{}, false, err
	}
	var quotaBlob []byte
	if len(input.RawQuota) > 0 {
		quotaBlob, err = store.box.Seal(input.RawQuota, []byte("quota:"+accountID))
		if err != nil {
			return Account{}, false, err
		}
	}
	now := time.Now().UTC()
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, false, err
	}
	defer transaction.Rollback()
	if duplicate {
		_, err = transaction.ExecContext(ctx, `
			UPDATE accounts SET credential_blob=?, masked_email=?, plan=?, status=?,
			quota_used_percent=?, quota_reset_at=?, reset_credits=?, raw_quota_blob=?, last_error='', updated_at=?
			WHERE id=?`, credentialBlob, input.MaskedEmail, input.Plan, input.Status,
			input.QuotaUsedPercent, unixTime(input.QuotaResetAt), input.ResetCredits, quotaBlob, now.Unix(), accountID)
	} else {
		var count, routeOrder int
		if countErr := transaction.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(route_order), -1) + 1 FROM accounts`).Scan(&count, &routeOrder); countErr != nil {
			return Account{}, false, countErr
		}
		_, err = transaction.ExecContext(ctx, `
			INSERT INTO accounts(id, stable_hash, credential_blob, masked_email, plan, status, primary_account,
			route_order, quota_used_percent, quota_reset_at, reset_credits, raw_quota_blob, created_at, updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, accountID, stableHash, credentialBlob, input.MaskedEmail,
			input.Plan, input.Status, boolInt(count == 0), routeOrder, input.QuotaUsedPercent, unixTime(input.QuotaResetAt),
			input.ResetCredits, quotaBlob, now.Unix(), now.Unix())
	}
	if err != nil {
		return Account{}, false, err
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM entitlements WHERE account_id=?`, accountID); err != nil {
		return Account{}, false, err
	}
	for _, modelID := range uniqueSorted(input.EntitledModels) {
		if _, err = transaction.ExecContext(ctx, `INSERT INTO entitlements(account_id, model_id) VALUES(?,?)`, accountID, modelID); err != nil {
			return Account{}, false, err
		}
	}
	if len(input.RawCatalogSnapshot) > 0 {
		if _, err = transaction.ExecContext(ctx, `
			INSERT INTO catalog_snapshots(provider, account_id, raw_json, fetched_at) VALUES('openai',?,?,?)
			ON CONFLICT(provider,account_id) DO UPDATE SET raw_json=excluded.raw_json, fetched_at=excluded.fetched_at`,
			accountID, []byte(input.RawCatalogSnapshot), now.Unix()); err != nil {
			return Account{}, false, err
		}
	}
	if err = transaction.Commit(); err != nil {
		return Account{}, false, err
	}
	account, err := store.Account(ctx, accountID, true)
	return account, duplicate, err
}

func (store *Store) Account(ctx context.Context, accountID string, includeCredential bool) (Account, error) {
	row := store.db.QueryRowContext(ctx, `
		SELECT id, credential_blob, masked_email, plan, status, paused, primary_account, route_order,
		quota_used_percent, quota_reset_at, reset_credits, raw_quota_blob, last_error, created_at, updated_at
		FROM accounts WHERE id=?`, accountID)
	account, err := store.scanAccount(row, includeCredential)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, err
	}
	account.EntitledModels, err = store.accountEntitlements(ctx, accountID)
	if err != nil {
		return Account{}, err
	}
	var raw []byte
	if snapshotErr := store.db.QueryRowContext(ctx, `SELECT raw_json FROM catalog_snapshots WHERE provider='openai' AND account_id=?`, accountID).Scan(&raw); snapshotErr == nil {
		account.RawCatalogSnapshot = raw
	} else if !errors.Is(snapshotErr, sql.ErrNoRows) {
		return Account{}, snapshotErr
	}
	return account, nil
}

func (store *Store) Accounts(ctx context.Context, includeCredentials bool) ([]Account, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT id, credential_blob, masked_email, plan, status, paused, primary_account, route_order,
		quota_used_percent, quota_reset_at, reset_credits, raw_quota_blob, last_error, created_at, updated_at
		FROM accounts ORDER BY primary_account DESC, route_order ASC, created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	var accounts []Account
	for rows.Next() {
		account, scanErr := store.scanAccount(rows, includeCredentials)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		accounts = append(accounts, account)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	// The store deliberately uses one SQLite connection. Finish the account
	// query before loading dependent rows so these lookups cannot deadlock.
	for index := range accounts {
		accounts[index].EntitledModels, err = store.accountEntitlements(ctx, accounts[index].ID)
		if err != nil {
			return nil, err
		}
		var raw []byte
		snapshotErr := store.db.QueryRowContext(ctx, `SELECT raw_json FROM catalog_snapshots WHERE provider='openai' AND account_id=?`, accounts[index].ID).Scan(&raw)
		if snapshotErr == nil {
			accounts[index].RawCatalogSnapshot = raw
		} else if !errors.Is(snapshotErr, sql.ErrNoRows) {
			return nil, snapshotErr
		}
	}
	return accounts, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (store *Store) scanAccount(row rowScanner, includeCredential bool) (Account, error) {
	var account Account
	var credentialBlob, quotaBlob []byte
	var paused, primary int
	var quotaReset, created, updated int64
	if err := row.Scan(&account.ID, &credentialBlob, &account.MaskedEmail, &account.Plan, &account.Status,
		&paused, &primary, &account.RouteOrder, &account.QuotaUsedPercent, &quotaReset, &account.ResetCredits, &quotaBlob,
		&account.LastError, &created, &updated); err != nil {
		return Account{}, err
	}
	account.Paused = paused != 0
	account.Primary = primary != 0
	account.QuotaResetAt = fromUnix(quotaReset)
	account.CreatedAt = fromUnix(created)
	account.UpdatedAt = fromUnix(updated)
	if includeCredential {
		plaintext, err := store.box.Open(credentialBlob, []byte("account:"+account.ID))
		if err != nil {
			return Account{}, fmt.Errorf("decrypt account credential: %w", err)
		}
		if err = json.Unmarshal(plaintext, &account.Credential); err != nil {
			return Account{}, fmt.Errorf("decode account credential: %w", err)
		}
	}
	if len(quotaBlob) > 0 {
		plaintext, err := store.box.Open(quotaBlob, []byte("quota:"+account.ID))
		if err != nil {
			return Account{}, fmt.Errorf("decrypt quota: %w", err)
		}
		account.RawQuota = plaintext
	}
	return account, nil
}

func (store *Store) accountEntitlements(ctx context.Context, accountID string) ([]string, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT model_id FROM entitlements WHERE account_id=? ORDER BY model_id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var modelIDs []string
	for rows.Next() {
		var modelID string
		if err = rows.Scan(&modelID); err != nil {
			return nil, err
		}
		modelIDs = append(modelIDs, modelID)
	}
	return modelIDs, rows.Err()
}

func (store *Store) UpdateAccountCredential(ctx context.Context, accountID string, credential OpenAICredential) error {
	encoded, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	sealed, err := store.box.Seal(encoded, []byte("account:"+accountID))
	if err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE accounts SET credential_blob=?, updated_at=? WHERE id=?`, sealed, time.Now().Unix(), accountID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (store *Store) UpdateAccountQuota(ctx context.Context, accountID, plan string, used float64, resetAt time.Time, resetCredits int, raw json.RawMessage) error {
	var sealed []byte
	var err error
	if len(raw) > 0 {
		sealed, err = store.box.Seal(raw, []byte("quota:"+accountID))
		if err != nil {
			return err
		}
	}
	result, err := store.db.ExecContext(ctx, `
		UPDATE accounts SET plan=?, quota_used_percent=?, quota_reset_at=?, reset_credits=?, raw_quota_blob=?,
		status='ready', last_error='', updated_at=? WHERE id=?`,
		plan, used, unixTime(resetAt), resetCredits, sealed, time.Now().Unix(), accountID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (store *Store) UpdateAccountCatalog(ctx context.Context, accountID string, raw json.RawMessage, modelIDs []string) error {
	if len(raw) == 0 {
		return errors.New("catalog snapshot is empty")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `UPDATE accounts SET status='ready', last_error='', updated_at=? WHERE id=?`, time.Now().Unix(), accountID)
	if err != nil {
		return err
	}
	if err = requireChanged(result); err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM entitlements WHERE account_id=?`, accountID); err != nil {
		return err
	}
	for _, modelID := range uniqueSorted(modelIDs) {
		if _, err = transaction.ExecContext(ctx, `INSERT INTO entitlements(account_id, model_id) VALUES(?,?)`, accountID, modelID); err != nil {
			return err
		}
	}
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO catalog_snapshots(provider, account_id, raw_json, fetched_at) VALUES('openai',?,?,?)
		ON CONFLICT(provider,account_id) DO UPDATE SET raw_json=excluded.raw_json, fetched_at=excluded.fetched_at`,
		accountID, []byte(raw), time.Now().Unix())
	if err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *Store) SetAccountStatus(ctx context.Context, accountID, status, lastError string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE accounts SET status=?, last_error=?, updated_at=? WHERE id=?`, status, lastError, time.Now().Unix(), accountID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (store *Store) MarkAccountExhausted(ctx context.Context, accountID string, resetAt time.Time) error {
	result, err := store.db.ExecContext(ctx, `
		UPDATE accounts SET quota_used_percent=100,
		quota_reset_at=CASE WHEN ? > 0 THEN ? ELSE quota_reset_at END,
		last_error='quota exhausted', updated_at=? WHERE id=?`,
		unixTime(resetAt), unixTime(resetAt), time.Now().Unix(), accountID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (store *Store) SetAccountPaused(ctx context.Context, accountID string, paused bool) error {
	result, err := store.db.ExecContext(ctx, `UPDATE accounts SET paused=?, updated_at=? WHERE id=?`, boolInt(paused), time.Now().Unix(), accountID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (store *Store) SetPrimaryAccount(ctx context.Context, accountID string) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var exists int
	if err = transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE id=?`, accountID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	if _, err = transaction.ExecContext(ctx, `UPDATE accounts SET primary_account=0`); err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, `UPDATE accounts SET primary_account=1, updated_at=? WHERE id=?`, time.Now().Unix(), accountID); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *Store) ReorderAccounts(ctx context.Context, accountIDs []string) error {
	if len(accountIDs) == 0 {
		return ErrInvalidAccountOrder
	}
	seen := make(map[string]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID == "" {
			return ErrInvalidAccountOrder
		}
		if _, duplicate := seen[accountID]; duplicate {
			return ErrInvalidAccountOrder
		}
		seen[accountID] = struct{}{}
	}

	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var count int
	if err = transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&count); err != nil {
		return err
	}
	if count != len(accountIDs) {
		return ErrInvalidAccountOrder
	}
	for routeOrder, accountID := range accountIDs {
		result, updateErr := transaction.ExecContext(ctx, `UPDATE accounts SET route_order=?, primary_account=? WHERE id=?`, routeOrder, boolInt(routeOrder == 0), accountID)
		if updateErr != nil {
			return updateErr
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if changed != 1 {
			return ErrInvalidAccountOrder
		}
	}
	return transaction.Commit()
}

func (store *Store) DeleteAccount(ctx context.Context, accountID string) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var wasPrimary int
	if err = transaction.QueryRowContext(ctx, `SELECT primary_account FROM accounts WHERE id=?`, accountID).Scan(&wasPrimary); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	result, err := transaction.ExecContext(ctx, `DELETE FROM accounts WHERE id=?`, accountID)
	if err != nil {
		return err
	}
	if err = requireChanged(result); err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM catalog_snapshots WHERE provider='openai' AND account_id=?`, accountID); err != nil {
		return err
	}
	if wasPrimary != 0 {
		_, err = transaction.ExecContext(ctx, `UPDATE accounts SET primary_account=1 WHERE id=(SELECT id FROM accounts ORDER BY route_order, created_at, id LIMIT 1)`)
		if err != nil {
			return err
		}
	}
	return transaction.Commit()
}

func requireChanged(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	output := make([]string, 0, len(seen))
	for value := range seen {
		output = append(output, value)
	}
	sort.Strings(output)
	return output
}
