package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (store *Store) AffinityAccount(ctx context.Context, affinityHash []byte, modelID string) (string, error) {
	var accountID string
	err := store.db.QueryRowContext(ctx, `SELECT account_id FROM affinities WHERE affinity_hash=? AND model_id=?`, affinityHash, modelID).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return accountID, err
}

func (store *Store) PutAffinity(ctx context.Context, affinityHash []byte, modelID, accountID string) error {
	_, err := store.db.ExecContext(ctx, `
		INSERT INTO affinities(affinity_hash, model_id, account_id, updated_at) VALUES(?,?,?,?)
		ON CONFLICT(affinity_hash,model_id) DO UPDATE SET account_id=excluded.account_id, updated_at=excluded.updated_at`,
		affinityHash, modelID, accountID, time.Now().Unix())
	return err
}

func (store *Store) DeleteAffinity(ctx context.Context, affinityHash []byte, modelID string) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM affinities WHERE affinity_hash=? AND model_id=?`, affinityHash, modelID)
	return err
}
