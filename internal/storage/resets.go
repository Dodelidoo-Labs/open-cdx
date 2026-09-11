package storage

import (
	"context"
	"encoding/json"
)

// InvalidateAccountResetCredits hides the stale bank without changing quota
// windows or account health when post-redemption collection is unavailable.
func (store *Store) InvalidateAccountResetCredits(ctx context.Context, accountID string) error {
	account, err := store.Account(ctx, accountID, false)
	if err != nil {
		return err
	}
	payload := map[string]json.RawMessage{}
	if len(account.RawQuota) > 0 {
		if err := json.Unmarshal(account.RawQuota, &payload); err != nil {
			return err
		}
	}
	payload["rate_limit_reset_credits"] = json.RawMessage("null")
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	sealed, err := store.box.Seal(raw, []byte("quota:"+accountID))
	if err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE accounts SET reset_credits=0, raw_quota_blob=? WHERE id=?`, sealed, accountID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}
