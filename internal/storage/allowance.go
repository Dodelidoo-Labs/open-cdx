package storage

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

// History identifies a device, never the billed account. Live quota polls
// identify an account, never the device consuming its allowance.
type AllowanceObservation struct {
	Source, AccountID, DeviceID, Label string
	ObservedAt, ResetAt                time.Time
	Used                               float64
	WindowSeconds                      int64
}

// Older observations and imported machine history describe the weekly window.
func (o AllowanceObservation) WindowDuration() time.Duration {
	if o.WindowSeconds == 0 {
		return 7 * 24 * time.Hour
	}
	return time.Duration(o.WindowSeconds) * time.Second
}

func (o AllowanceObservation) Validate(now time.Time) error {
	if o.WindowSeconds < 0 || o.WindowSeconds > 366*86400 || o.ObservedAt.IsZero() || o.ObservedAt.After(now.Add(5*time.Minute)) || o.ResetAt.Before(o.ObservedAt.Add(-5*time.Minute)) || o.ResetAt.After(o.ObservedAt.Add(o.WindowDuration()+5*time.Minute)) || math.IsNaN(o.Used) || math.IsInf(o.Used, 0) || o.Used < 0 || o.Used > 100 {
		return errors.New("invalid allowance observation")
	}
	return nil
}

type allowanceExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertAllowanceObservation(ctx context.Context, db allowanceExecutor, o AllowanceObservation) error {
	if err := o.Validate(time.Now()); err != nil {
		return err
	}
	if (o.Source != "live" || o.AccountID == "" || o.DeviceID != "") && (o.Source != "history" || o.DeviceID == "" || o.AccountID != "") {
		return errors.New("invalid allowance observation scope")
	}
	_, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO allowance_observations(source,account_id,device_id,observed_at,reset_at,used_percent,window_seconds) VALUES(?,?,?,?,?,?,?)`, o.Source, o.AccountID, o.DeviceID, o.ObservedAt.UTC().Format(time.RFC3339Nano), o.ResetAt.UTC().Format(time.RFC3339Nano), o.Used, int64(o.WindowDuration()/time.Second))
	return err
}

func (store *Store) RecordAllowanceObservation(ctx context.Context, o AllowanceObservation) error {
	o.Source, o.DeviceID = "live", ""
	if err := insertAllowanceObservation(ctx, store.db, o); err != nil {
		return err
	}
	store.telemetryRevision.Add(1)
	return nil
}

func (store *Store) AllowanceObservations(ctx context.Context) ([]AllowanceObservation, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT o.source,o.account_id,o.device_id,o.observed_at,o.reset_at,o.used_percent,o.window_seconds,CASE WHEN o.source='live' THEN COALESCE(a.masked_email,'Removed account') ELSE COALESCE(d.name,'Removed machine') END FROM allowance_observations o LEFT JOIN accounts a ON a.id=o.account_id LEFT JOIN devices d ON d.id=o.device_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AllowanceObservation, 0)
	for rows.Next() {
		var o AllowanceObservation
		var observed, reset string
		if err = rows.Scan(&o.Source, &o.AccountID, &o.DeviceID, &observed, &reset, &o.Used, &o.WindowSeconds, &o.Label); err != nil {
			return nil, err
		}
		if o.ObservedAt, err = time.Parse(time.RFC3339Nano, observed); err != nil {
			return nil, err
		}
		if o.ResetAt, err = time.Parse(time.RFC3339Nano, reset); err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

// Rebuild the primary key once, keeping all pre-overlay observations as weekly.
func (store *Store) migrateAllowanceWindows(ctx context.Context) error {
	present, err := store.tableHasColumn(ctx, "allowance_observations", "window_seconds")
	if err != nil || present {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE allowance_observations_next (
			source TEXT NOT NULL CHECK(source IN ('live','history')),
			account_id TEXT NOT NULL DEFAULT '', device_id TEXT NOT NULL DEFAULT '',
			observed_at TEXT NOT NULL, reset_at TEXT NOT NULL, used_percent REAL NOT NULL,
			window_seconds INTEGER NOT NULL DEFAULT 604800,
			PRIMARY KEY(source,account_id,device_id,window_seconds,observed_at,reset_at,used_percent)
		);
		INSERT INTO allowance_observations_next SELECT source,account_id,device_id,observed_at,reset_at,used_percent,604800 FROM allowance_observations;
		DROP TABLE allowance_observations;
		ALTER TABLE allowance_observations_next RENAME TO allowance_observations;
	`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
