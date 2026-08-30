package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
)

func (store *Store) CreateOAuthTransaction(ctx context.Context, deviceID, state, verifier, redirectURI string, expiresAt time.Time) (OAuthTransaction, error) {
	transactionID, err := secure.RandomURLSafe(18)
	if err != nil {
		return OAuthTransaction{}, err
	}
	verifierBlob, err := store.box.Seal([]byte(verifier), []byte("oauth:"+transactionID))
	if err != nil {
		return OAuthTransaction{}, err
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO oauth_transactions(id, device_id, state_hash, verifier_blob, redirect_uri, expires_at, created_at)
		VALUES(?,?,?,?,?,?,?)`, transactionID, deviceID, secure.Digest(state), verifierBlob, redirectURI,
		expiresAt.Unix(), time.Now().Unix())
	if err != nil {
		return OAuthTransaction{}, err
	}
	return OAuthTransaction{ID: transactionID, DeviceID: deviceID, State: state, Verifier: verifier, RedirectURI: redirectURI, ExpiresAt: expiresAt}, nil
}

func (store *Store) ConsumeOAuthTransaction(ctx context.Context, transactionID, deviceID, state string, now time.Time) (OAuthTransaction, error) {
	databaseTransaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthTransaction{}, err
	}
	defer databaseTransaction.Rollback()
	var transaction OAuthTransaction
	var stateHash, verifierBlob []byte
	var expires, used int64
	err = databaseTransaction.QueryRowContext(ctx, `
		SELECT id, device_id, state_hash, verifier_blob, redirect_uri, expires_at, used_at
		FROM oauth_transactions WHERE id=?`, transactionID).Scan(&transaction.ID, &transaction.DeviceID,
		&stateHash, &verifierBlob, &transaction.RedirectURI, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthTransaction{}, ErrOAuthInvalid
	}
	if err != nil {
		return OAuthTransaction{}, err
	}
	if transaction.DeviceID != deviceID || used != 0 || now.Unix() >= expires || !secure.EqualDigest(stateHash, state) {
		return OAuthTransaction{}, ErrOAuthInvalid
	}
	verifier, err := store.box.Open(verifierBlob, []byte("oauth:"+transaction.ID))
	if err != nil {
		return OAuthTransaction{}, err
	}
	result, err := databaseTransaction.ExecContext(ctx, `UPDATE oauth_transactions SET used_at=? WHERE id=? AND used_at=0`, now.Unix(), transactionID)
	if err != nil {
		return OAuthTransaction{}, err
	}
	if err = requireChanged(result); err != nil {
		return OAuthTransaction{}, ErrOAuthInvalid
	}
	if err = databaseTransaction.Commit(); err != nil {
		return OAuthTransaction{}, err
	}
	transaction.State = state
	transaction.Verifier = string(verifier)
	transaction.ExpiresAt = fromUnix(expires)
	return transaction, nil
}

func (store *Store) PruneOAuthTransactions(ctx context.Context, before time.Time) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM oauth_transactions WHERE expires_at < ? OR (used_at != 0 AND used_at < ?)`, before.Unix(), before.Add(-time.Hour).Unix())
	return err
}
