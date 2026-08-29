package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	secure "github.com/opencdx/opencdx/internal/crypto"
)

type Store struct {
	db  *sql.DB
	box *secure.Box
}

func Open(path string, box *secure.Box) (*Store, error) {
	if box == nil {
		return nil, errors.New("encrypted storage requires a credential box")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	dsn := path
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		dsn = "file:" + filepath.Clean(path) + "?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL"
	}
	database, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetConnMaxLifetime(0)
	store := &Store{db: database, box: box}
	if err := store.migrate(context.Background()); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() error {
	return store.db.Close()
}

func (store *Store) Database() *sql.DB {
	return store.db
}

func (store *Store) migrate(ctx context.Context) error {
	if _, err := store.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	routeOrderPresent, err := store.tableHasColumn(ctx, "accounts", "route_order")
	if err != nil {
		return fmt.Errorf("inspect account order migration: %w", err)
	}
	if !routeOrderPresent {
		if _, err = store.db.ExecContext(ctx, "ALTER TABLE accounts ADD COLUMN route_order INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("migrate account route order: %w", err)
		}
		// Preserve the deterministic order used before route ordering became
		// configurable. The ID tie-breaker also handles identical timestamps.
		if _, err = store.db.ExecContext(ctx, `
			UPDATE accounts SET route_order = (
				SELECT COUNT(*) FROM accounts AS preceding
				WHERE preceding.created_at < accounts.created_at
				   OR (preceding.created_at = accounts.created_at AND preceding.id < accounts.id)
			)`); err != nil {
			return fmt.Errorf("initialize account route order: %w", err)
		}
	}
	// Version-one installations predate the detailed token counters. SQLite
	// does not support ADD COLUMN IF NOT EXISTS, so inspect before altering.
	for _, column := range []string{"cached_input_tokens", "cache_write_input_tokens", "reasoning_output_tokens"} {
		present, err := store.tableHasColumn(ctx, "usage_aggregate", column)
		if err != nil {
			return fmt.Errorf("inspect database migration: %w", err)
		}
		if !present {
			if _, err = store.db.ExecContext(ctx, "ALTER TABLE usage_aggregate ADD COLUMN "+column+" INTEGER NOT NULL DEFAULT 0"); err != nil {
				return fmt.Errorf("migrate database column %s: %w", column, err)
			}
		}
	}
	return nil
}

func (store *Store) tableHasColumn(ctx context.Context, table, column string) (bool, error) {
	rows, err := store.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err = rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func unixTime(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.Unix()
}

func fromUnix(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}
