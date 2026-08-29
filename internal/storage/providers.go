package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (store *Store) PutProvider(ctx context.Context, provider ProviderConfig) error {
	var secretBlob []byte
	var err error
	if provider.APIKey != "" {
		secretBlob, err = store.box.Seal([]byte(provider.APIKey), []byte("provider:"+provider.Name))
		if err != nil {
			return err
		}
	} else {
		var existing []byte
		if queryErr := store.db.QueryRowContext(ctx, `SELECT secret_blob FROM providers WHERE name=?`, provider.Name).Scan(&existing); queryErr == nil {
			secretBlob = existing
		} else if !errors.Is(queryErr, sql.ErrNoRows) {
			return queryErr
		}
	}
	if provider.Config == nil {
		provider.Config = json.RawMessage(`{}`)
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO providers(name, base_url, enabled, secret_blob, config_json, health, last_error, updated_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET base_url=excluded.base_url, enabled=excluded.enabled,
		secret_blob=excluded.secret_blob, config_json=excluded.config_json, health=excluded.health,
		last_error=excluded.last_error, updated_at=excluded.updated_at`,
		provider.Name, provider.BaseURL, boolInt(provider.Enabled), secretBlob, []byte(provider.Config),
		provider.Health, provider.LastError, time.Now().Unix())
	return err
}

func (store *Store) Provider(ctx context.Context, name string, includeSecret bool) (ProviderConfig, error) {
	var provider ProviderConfig
	var enabled int
	var secretBlob, configJSON []byte
	var updated int64
	err := store.db.QueryRowContext(ctx, `
		SELECT name, base_url, enabled, secret_blob, config_json, health, last_error, updated_at
		FROM providers WHERE name=?`, name).Scan(&provider.Name, &provider.BaseURL, &enabled, &secretBlob,
		&configJSON, &provider.Health, &provider.LastError, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderConfig{}, ErrNotFound
	}
	if err != nil {
		return ProviderConfig{}, err
	}
	provider.Enabled = enabled != 0
	provider.Config = configJSON
	provider.UpdatedAt = fromUnix(updated)
	if includeSecret && len(secretBlob) > 0 {
		secret, openErr := store.box.Open(secretBlob, []byte("provider:"+name))
		if openErr != nil {
			return ProviderConfig{}, openErr
		}
		provider.APIKey = string(secret)
	}
	return provider, nil
}

func (store *Store) Providers(ctx context.Context) ([]ProviderConfig, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT name FROM providers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	var providers []ProviderConfig
	for _, name := range names {
		provider, providerErr := store.Provider(ctx, name, false)
		if providerErr != nil {
			return nil, providerErr
		}
		providers = append(providers, provider)
	}
	return providers, nil
}

func (store *Store) ProviderHasSecret(ctx context.Context, name string) (bool, error) {
	var present int
	err := store.db.QueryRowContext(ctx, `SELECT CASE WHEN length(secret_blob) > 0 THEN 1 ELSE 0 END FROM providers WHERE name=?`, name).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return present != 0, err
}

func (store *Store) SetProviderHealth(ctx context.Context, name, health, lastError string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE providers SET health=?, last_error=?, updated_at=? WHERE name=?`, health, lastError, time.Now().Unix(), name)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (store *Store) ClearProviderSecret(ctx context.Context, name string) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `UPDATE providers SET secret_blob=NULL, enabled=0, health='unconfigured', last_error='', updated_at=? WHERE name=?`, time.Now().Unix(), name)
	if err != nil {
		return err
	}
	if err = requireChanged(result); err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM catalog_snapshots WHERE provider=?`, name); err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM catalog_exclusions WHERE provider=?`, name); err != nil {
		return err
	}
	return transaction.Commit()
}
