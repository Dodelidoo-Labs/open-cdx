package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

func (store *Store) PutCatalogSnapshot(ctx context.Context, snapshot CatalogSnapshot) error {
	_, err := store.db.ExecContext(ctx, `
		INSERT INTO catalog_snapshots(provider, account_id, etag, raw_json, fetched_at) VALUES(?,?,?,?,?)
		ON CONFLICT(provider,account_id) DO UPDATE SET etag=excluded.etag, raw_json=excluded.raw_json,
		fetched_at=excluded.fetched_at`, snapshot.Provider, snapshot.AccountID, snapshot.ETag,
		[]byte(snapshot.Raw), unixTime(snapshot.FetchedAt))
	return err
}

func (store *Store) CatalogSnapshots(ctx context.Context, provider string) ([]CatalogSnapshot, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT provider, account_id, etag, raw_json, fetched_at FROM catalog_snapshots
		WHERE provider=? ORDER BY account_id`, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var snapshots []CatalogSnapshot
	for rows.Next() {
		var snapshot CatalogSnapshot
		var fetched int64
		if err = rows.Scan(&snapshot.Provider, &snapshot.AccountID, &snapshot.ETag, &snapshot.Raw, &fetched); err != nil {
			return nil, err
		}
		snapshot.FetchedAt = fromUnix(fetched)
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

func (store *Store) PutMergedCatalog(ctx context.Context, deviceID, codexVersion string, raw []byte) (string, error) {
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	_, err := store.db.ExecContext(ctx, `
		INSERT INTO merged_catalogs(device_id, codex_version, content_hash, raw_json, created_at) VALUES(?,?,?,?,?)
		ON CONFLICT(device_id,codex_version) DO UPDATE SET content_hash=excluded.content_hash,
		raw_json=excluded.raw_json, created_at=excluded.created_at`, deviceID, codexVersion, hash, raw, time.Now().Unix())
	return hash, err
}

func (store *Store) MergedCatalog(ctx context.Context, deviceID, codexVersion string) ([]byte, string, time.Time, error) {
	var raw []byte
	var hash string
	var created int64
	err := store.db.QueryRowContext(ctx, `
		SELECT raw_json, content_hash, created_at FROM merged_catalogs WHERE device_id=? AND codex_version=?`,
		deviceID, codexVersion).Scan(&raw, &hash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", time.Time{}, ErrNotFound
	}
	return raw, hash, fromUnix(created), err
}

func (store *Store) ReplaceExclusions(ctx context.Context, provider string, exclusions []CatalogExclusion) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err = transaction.ExecContext(ctx, `DELETE FROM catalog_exclusions WHERE provider=?`, provider); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, exclusion := range exclusions {
		if _, err = transaction.ExecContext(ctx, `INSERT INTO catalog_exclusions(provider, model_id, reason, updated_at) VALUES(?,?,?,?)`,
			provider, exclusion.ModelID, exclusion.Reason, now); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

func (store *Store) Exclusions(ctx context.Context) ([]CatalogExclusion, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT provider, model_id, reason FROM catalog_exclusions ORDER BY provider, model_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var exclusions []CatalogExclusion
	for rows.Next() {
		var exclusion CatalogExclusion
		if err = rows.Scan(&exclusion.Provider, &exclusion.ModelID, &exclusion.Reason); err != nil {
			return nil, err
		}
		exclusions = append(exclusions, exclusion)
	}
	return exclusions, rows.Err()
}

func (store *Store) ReplaceConflicts(ctx context.Context, conflicts map[string]string) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err = transaction.ExecContext(ctx, `DELETE FROM catalog_conflicts`); err != nil {
		return err
	}
	for modelID, detail := range conflicts {
		if _, err = transaction.ExecContext(ctx, `INSERT INTO catalog_conflicts(model_id, detail, updated_at) VALUES(?,?,?)`, modelID, detail, time.Now().Unix()); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

func (store *Store) Conflicts(ctx context.Context) (map[string]string, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT model_id, detail FROM catalog_conflicts ORDER BY model_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	conflicts := make(map[string]string)
	for rows.Next() {
		var modelID, detail string
		if err = rows.Scan(&modelID, &detail); err != nil {
			return nil, err
		}
		conflicts[modelID] = detail
	}
	return conflicts, rows.Err()
}
