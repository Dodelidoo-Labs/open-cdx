package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (store *Store) RecordUsage(ctx context.Context, provider, modelID, accountID string, inputTokens, outputTokens int64) error {
	day := time.Now().UTC().Format("2006-01-02")
	_, err := store.db.ExecContext(ctx, `
		INSERT INTO usage_aggregate(day, provider, model_id, account_id, source, routing, requests, input_tokens, output_tokens)
		VALUES(?,?,?,?,?,?,1,?,?)
		ON CONFLICT(day,provider,model_id,account_id,routing) DO UPDATE SET requests=requests+1,
		input_tokens=input_tokens+excluded.input_tokens, output_tokens=output_tokens+excluded.output_tokens`,
		day, provider, modelID, accountID, UsageSourceRouted, UsageRoutingRouted, inputTokens, outputTokens)
	if err == nil {
		store.telemetryRevision.Add(1)
	}
	return err
}

// TelemetryRevision returns the process-scoped seed and current successful
// mutation revision used to build opaque conditional response validators.
func (store *Store) TelemetryRevision() (string, uint64) {
	return store.telemetrySeed, store.telemetryRevision.Load()
}

// ResetTelemetry transactionally removes only aggregate usage and its
// reconciliation metadata. Provider, device, account, catalog, and routing
// state are deliberately outside this transaction.
func (store *Store) ResetTelemetry(ctx context.Context) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err = transaction.ExecContext(ctx, `DELETE FROM usage_aggregate`); err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM usage_reconciliation`); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return err
	}
	store.telemetryRevision.Add(1)
	return nil
}

// ReplaceUsage transactionally replaces all telemetry with a local history
// snapshot. The synthetic account value carries no local or remote identity.
// Requests recorded by the proxy after this transaction commits continue to
// accumulate normally.
func (store *Store) ReplaceUsage(ctx context.Context, usage []UsageAggregate, reconciliation UsageReconciliation) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err = transaction.ExecContext(ctx, `DELETE FROM usage_aggregate`); err != nil {
		return err
	}
	statement, err := transaction.PrepareContext(ctx, `
		INSERT INTO usage_aggregate(day, provider, model_id, account_id, source, routing, requests, input_tokens,
		cached_input_tokens, cache_write_input_tokens, output_tokens, reasoning_output_tokens)
		VALUES(?,?,?,'reconciled-history',?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	for _, aggregate := range usage {
		if _, err = statement.ExecContext(ctx, aggregate.Day, aggregate.Provider, aggregate.ModelID, UsageSourceReconciled, aggregate.Routing, aggregate.Requests,
			aggregate.InputTokens, aggregate.CachedInputTokens, aggregate.CacheWriteInputTokens,
			aggregate.OutputTokens, aggregate.ReasoningOutputTokens); err != nil {
			_ = statement.Close()
			return err
		}
	}
	if err = statement.Close(); err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, `
		INSERT INTO usage_reconciliation(singleton, reconciled_at, files_scanned, events_imported, rows_imported)
		VALUES(1,?,?,?,?)
		ON CONFLICT(singleton) DO UPDATE SET reconciled_at=excluded.reconciled_at,
		files_scanned=excluded.files_scanned, events_imported=excluded.events_imported,
		rows_imported=excluded.rows_imported`, unixTime(reconciliation.ReconciledAt), reconciliation.FilesScanned,
		reconciliation.EventsImported, reconciliation.RowsImported); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return err
	}
	store.telemetryRevision.Add(1)
	return nil
}

func (store *Store) UsageReconciliation(ctx context.Context) (UsageReconciliation, error) {
	var result UsageReconciliation
	var reconciledAt int64
	err := store.db.QueryRowContext(ctx, `SELECT reconciled_at, files_scanned, events_imported, rows_imported
		FROM usage_reconciliation WHERE singleton=1`).Scan(&reconciledAt, &result.FilesScanned, &result.EventsImported, &result.RowsImported)
	if errors.Is(err, sql.ErrNoRows) {
		return UsageReconciliation{}, ErrNotFound
	}
	if err != nil {
		return UsageReconciliation{}, err
	}
	result.ReconciledAt = fromUnix(reconciledAt)
	return result, nil
}

func (store *Store) Usage(ctx context.Context, since time.Time) ([]UsageAggregate, error) {
	query := `SELECT day, provider, model_id, account_id, source, routing, requests, input_tokens, cached_input_tokens,
		cache_write_input_tokens, output_tokens, reasoning_output_tokens FROM usage_aggregate`
	args := make([]any, 0, 1)
	if !since.IsZero() {
		query += ` WHERE day>=?`
		args = append(args, since.UTC().Format("2006-01-02"))
	}
	rows, err := store.db.QueryContext(ctx, query+` ORDER BY day DESC, provider, model_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var usage []UsageAggregate
	for rows.Next() {
		var aggregate UsageAggregate
		if err = rows.Scan(&aggregate.Day, &aggregate.Provider, &aggregate.ModelID, &aggregate.AccountID, &aggregate.Source, &aggregate.Routing,
			&aggregate.Requests, &aggregate.InputTokens, &aggregate.CachedInputTokens,
			&aggregate.CacheWriteInputTokens, &aggregate.OutputTokens, &aggregate.ReasoningOutputTokens); err != nil {
			return nil, err
		}
		usage = append(usage, aggregate)
	}
	return usage, rows.Err()
}
