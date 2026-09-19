package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAllowanceObservationIsolationAndReset(t *testing.T) {
	store := testStore(t, filepath.Join(t.TempDir(), "allowance.db"))
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	o := AllowanceObservation{AccountID: "account", ObservedAt: now, ResetAt: now.Add(7 * 24 * time.Hour), Used: 80}
	_, before := store.TelemetryRevision()
	if err := store.RecordAllowanceObservation(ctx, o); err != nil {
		t.Fatal(err)
	}
	_, after := store.TelemetryRevision()
	if after <= before {
		t.Fatal("live quota did not invalidate report")
	}
	if err := store.RecordAllowanceObservation(ctx, o); err != nil {
		t.Fatal(err)
	}
	for _, device := range []string{"a", "b", "a"} {
		if err := store.ReplaceUsage(ctx, device, nil, UsageReconciliation{ReconciledAt: now}, []AllowanceObservation{o}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.AllowanceObservations(ctx)
	if err != nil || len(rows) != 3 {
		t.Fatalf("idempotent isolated observations: %#v %v", rows, err)
	}
	for _, row := range rows {
		if row.Source == "history" && row.AccountID != "" {
			t.Fatal("import trusted account identity")
		}
	}
	if err := store.ReplaceUsage(ctx, "a", nil, UsageReconciliation{ReconciledAt: now}); err != nil {
		t.Fatal(err)
	}
	rows, _ = store.AllowanceObservations(ctx)
	if len(rows) != 3 {
		t.Fatal("old helper erased observation history")
	}
	if err := store.ReplaceUsage(ctx, "a", nil, UsageReconciliation{ReconciledAt: now}, []AllowanceObservation{}); err != nil {
		t.Fatal(err)
	}
	rows, _ = store.AllowanceObservations(ctx)
	if len(rows) != 2 {
		t.Fatal("explicit empty snapshot did not clear device observations")
	}
	if err := store.ResetTelemetry(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err = store.AllowanceObservations(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatalf("reset: %#v %v", rows, err)
	}
}

func TestAllowanceWindowMigrationPreservesHistoryAndSeparatesWindows(t *testing.T) {
	store := testStore(t, filepath.Join(t.TempDir(), "legacy.db"))
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	// A pre-overlay installation with both live and imported weekly readings.
	_, err := store.db.Exec(`DROP TABLE allowance_observations;
 CREATE TABLE allowance_observations(source TEXT,account_id TEXT,device_id TEXT,observed_at TEXT,reset_at TEXT,used_percent REAL,PRIMARY KEY(source,account_id,device_id,observed_at,reset_at,used_percent));`)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"live", "history"} {
		_, err = store.db.Exec(`INSERT INTO allowance_observations VALUES(?,?,?,?,?,?)`, source, "account", "", now.Format(time.RFC3339Nano), now.Add(time.Hour).Format(time.RFC3339Nano), 50)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err = store.migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	short := AllowanceObservation{AccountID: "account", ObservedAt: now, ResetAt: now.Add(time.Hour), Used: 50, WindowSeconds: 18000}
	if err = store.RecordAllowanceObservation(ctx, short); err != nil {
		t.Fatal(err)
	}
	rows, err := store.AllowanceObservations(ctx)
	if err != nil || len(rows) != 3 {
		t.Fatalf("history/windows lost: %#v %v", rows, err)
	}
	counts := map[int64]int{}
	for _, row := range rows {
		counts[row.WindowSeconds]++
	}
	if counts[604800] != 2 || counts[18000] != 1 {
		t.Fatal(counts)
	}
	if err = store.ResetTelemetry(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err = store.AllowanceObservations(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatalf("overlay survived telemetry reset: %v %v", rows, err)
	}
}
