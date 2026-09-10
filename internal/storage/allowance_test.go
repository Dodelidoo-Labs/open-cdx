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
