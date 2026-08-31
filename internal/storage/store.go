package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
)

type Store struct {
	db                *sql.DB
	box               *secure.Box
	telemetrySeed     string
	telemetryRevision atomic.Uint64
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
	telemetrySeed, err := secure.RandomURLSafe(18)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("initialize telemetry revision: %w", err)
	}
	store := &Store{db: database, box: box, telemetrySeed: telemetrySeed}
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
	sourcePresent, err := store.tableHasColumn(ctx, "usage_aggregate", "source")
	if err != nil {
		return fmt.Errorf("inspect usage source migration: %w", err)
	}
	if !sourcePresent {
		if _, err = store.db.ExecContext(ctx, "ALTER TABLE usage_aggregate ADD COLUMN source TEXT NOT NULL DEFAULT 'routed'"); err != nil {
			return fmt.Errorf("migrate usage source: %w", err)
		}
	}
	if err = store.migrateUsageRouting(ctx); err != nil {
		return err
	}
	return nil
}

func (store *Store) migrateUsageRouting(ctx context.Context) error {
	routingPresent, err := store.tableHasColumn(ctx, "usage_aggregate", "routing")
	if err != nil {
		return fmt.Errorf("inspect usage routing migration: %w", err)
	}
	primaryKey, err := store.tablePrimaryKeyColumns(ctx, "usage_aggregate")
	if err != nil {
		return fmt.Errorf("inspect usage routing key: %w", err)
	}
	expectedKey := []string{"day", "provider", "model_id", "account_id", "routing"}
	keyMatches := len(primaryKey) == len(expectedKey)
	for index := range primaryKey {
		if !keyMatches || primaryKey[index] != expectedKey[index] {
			keyMatches = false
			break
		}
	}
	if routingPresent && keyMatches {
		return nil
	}

	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin usage routing migration: %w", err)
	}
	defer transaction.Rollback()
	if _, err = transaction.ExecContext(ctx, `
		CREATE TABLE usage_aggregate_next (
			day TEXT NOT NULL,
			provider TEXT NOT NULL,
			model_id TEXT NOT NULL,
			account_id TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT 'routed' CHECK(source IN ('routed','reconciled')),
			routing TEXT NOT NULL DEFAULT 'routed' CHECK(routing IN ('routed','native')),
			requests INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			cached_input_tokens INTEGER NOT NULL DEFAULT 0,
			cache_write_input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			reasoning_output_tokens INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (day, provider, model_id, account_id, routing)
		)`); err != nil {
		return fmt.Errorf("create usage routing table: %w", err)
	}
	routingExpression := "CASE WHEN source='routed' THEN 'routed' ELSE 'native' END"
	if routingPresent {
		routingExpression = "CASE WHEN routing='native' THEN 'native' ELSE 'routed' END"
	}
	if _, err = transaction.ExecContext(ctx, `
		INSERT INTO usage_aggregate_next(day, provider, model_id, account_id, source, routing, requests,
			input_tokens, cached_input_tokens, cache_write_input_tokens, output_tokens, reasoning_output_tokens)
		SELECT day, provider, model_id, account_id,
			CASE WHEN source='reconciled' THEN 'reconciled' ELSE 'routed' END, `+routingExpression+`, requests,
			input_tokens, cached_input_tokens, cache_write_input_tokens, output_tokens, reasoning_output_tokens
		FROM usage_aggregate`); err != nil {
		return fmt.Errorf("copy usage routing data: %w", err)
	}
	if _, err = transaction.ExecContext(ctx, "DROP TABLE usage_aggregate"); err != nil {
		return fmt.Errorf("replace usage routing table: %w", err)
	}
	if _, err = transaction.ExecContext(ctx, "ALTER TABLE usage_aggregate_next RENAME TO usage_aggregate"); err != nil {
		return fmt.Errorf("rename usage routing table: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit usage routing migration: %w", err)
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

func (store *Store) tablePrimaryKeyColumns(ctx context.Context, table string) ([]string, error) {
	rows, err := store.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	positions := make(map[int]string)
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err = rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		if primaryKey > 0 {
			positions[primaryKey] = name
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	columns := make([]string, len(positions))
	for position, name := range positions {
		if position < 1 || position > len(columns) {
			return nil, errors.New("database has an invalid primary key")
		}
		columns[position-1] = name
	}
	return columns, nil
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
