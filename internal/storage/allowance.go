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
}

func (o AllowanceObservation) Validate(now time.Time) error {
	if o.ObservedAt.IsZero() || o.ObservedAt.After(now.Add(5*time.Minute)) || o.ResetAt.Before(o.ObservedAt.Add(-5*time.Minute)) || o.ResetAt.After(o.ObservedAt.Add(7*24*time.Hour+5*time.Minute)) || math.IsNaN(o.Used) || math.IsInf(o.Used, 0) || o.Used < 0 || o.Used > 100 {
		return errors.New("invalid weekly allowance observation")
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
	_, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO allowance_observations(source,account_id,device_id,observed_at,reset_at,used_percent) VALUES(?,?,?,?,?,?)`, o.Source, o.AccountID, o.DeviceID, o.ObservedAt.UTC().Format(time.RFC3339Nano), o.ResetAt.UTC().Format(time.RFC3339Nano), o.Used)
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
	rows, err := store.db.QueryContext(ctx, `SELECT o.source,o.account_id,o.device_id,o.observed_at,o.reset_at,o.used_percent,CASE WHEN o.source='live' THEN COALESCE(a.masked_email,'Removed account') ELSE COALESCE(d.name,'Removed machine') END FROM allowance_observations o LEFT JOIN accounts a ON a.id=o.account_id LEFT JOIN devices d ON d.id=o.device_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AllowanceObservation, 0)
	for rows.Next() {
		var o AllowanceObservation
		var observed, reset string
		if err = rows.Scan(&o.Source, &o.AccountID, &o.DeviceID, &observed, &reset, &o.Used, &o.Label); err != nil {
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
