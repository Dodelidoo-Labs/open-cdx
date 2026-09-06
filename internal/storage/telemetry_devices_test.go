package storage

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestReconciliationReplacesOnlyItsDeviceAndRollsBackOnFailure(t *testing.T) {
	store := testStore(t, ":memory:")
	ctx := context.Background()
	for _, device := range []string{"", "mac-a", "mac-b"} {
		if err := store.RecordUsage(ctx, device, "openai", "same-model", "same-account", 10, 5); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := []UsageAggregate{{Day: "2026-09-01", Provider: "openai", ModelID: "same-model", Routing: UsageRoutingRouted, Requests: 2, InputTokens: 20, CachedInputTokens: 7}}
	metadata := UsageReconciliation{ReconciledAt: time.Now(), EventsImported: 2, RowsImported: 1}
	for _, device := range []string{"mac-a", "mac-b", "mac-a"} {
		// Even a supplied row identity cannot redirect the replacement.
		snapshot[0].DeviceID = "not-the-authenticated-device"
		if err := store.ReplaceUsage(ctx, device, snapshot, metadata); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.Usage(ctx, time.Time{})
	if err != nil || len(before) != 3 {
		t.Fatalf("usage=%#v err=%v", before, err)
	}
	for _, row := range before {
		if row.DeviceID == "" {
			if row.Requests != 1 || row.Source != UsageSourceRouted {
				t.Fatalf("legacy usage changed: %#v", row)
			}
		} else if row.Requests != 2 || row.CachedInputTokens != 7 || row.Source != UsageSourceReconciled {
			t.Fatalf("device history was merged or lost: %#v", row)
		}
	}
	var metadataCount int
	if err := store.db.QueryRow(`SELECT count(*) FROM usage_reconciliation`).Scan(&metadataCount); err != nil || metadataCount != 2 {
		t.Fatalf("metadata count=%d err=%v", metadataCount, err)
	}
	if err := store.ReplaceUsage(ctx, "mac-a", append(snapshot, snapshot[0]), UsageReconciliation{ReconciledAt: time.Now().Add(time.Hour)}); err == nil {
		t.Fatal("duplicate import should fail")
	}
	after, err := store.Usage(ctx, time.Time{})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed import changed usage: %#v, %v", after, err)
	}
	var events int
	if err := store.db.QueryRow(`SELECT events_imported FROM usage_reconciliation WHERE device_id='mac-a'`).Scan(&events); err != nil || events != 2 {
		t.Fatalf("failed import changed metadata: %d, %v", events, err)
	}
	if err := store.RecordUsage(ctx, "mac-b", "openai", "same-model", "same-account", 3, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceUsage(ctx, "mac-a", nil, metadata); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.Usage(ctx, time.Time{})
	if err != nil || len(remaining) != 3 {
		t.Fatalf("empty A reconciliation affected B: %#v, %v", remaining, err)
	}
	for _, row := range remaining {
		if row.DeviceID == "mac-a" {
			t.Fatal("A was not replaced")
		}
	}
	if err := store.ResetTelemetry(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT count(*) FROM usage_reconciliation`).Scan(&metadataCount); err != nil || metadataCount != 0 {
		t.Fatalf("global reset retained metadata: %d, %v", metadataCount, err)
	}
}

func TestDeviceTelemetryMigrationPreservesLegacyAcrossStartupMigrations(t *testing.T) {
	store := testStore(t, ":memory:")
	ctx := context.Background()
	_, err := store.db.Exec(`
        DROP TABLE usage_aggregate;
        CREATE TABLE usage_aggregate (
            day TEXT NOT NULL, provider TEXT NOT NULL, model_id TEXT NOT NULL, account_id TEXT NOT NULL,
            source TEXT NOT NULL DEFAULT 'routed', routing TEXT NOT NULL DEFAULT 'routed',
            requests INTEGER NOT NULL, input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL,
            cached_input_tokens INTEGER NOT NULL DEFAULT 0, cache_write_input_tokens INTEGER NOT NULL DEFAULT 0,
            reasoning_output_tokens INTEGER NOT NULL DEFAULT 0,
            PRIMARY KEY(day,provider,model_id,account_id,routing));
        INSERT INTO usage_aggregate VALUES('2026-09-01','openai','model','account','reconciled','native',2,20,10,7,3,2);
        DROP TABLE usage_reconciliation;
        CREATE TABLE usage_reconciliation(singleton INTEGER PRIMARY KEY CHECK(singleton=1), reconciled_at INTEGER NOT NULL, files_scanned INTEGER NOT NULL, events_imported INTEGER NOT NULL, rows_imported INTEGER NOT NULL);
        INSERT INTO usage_reconciliation VALUES(1,100,3,2,1);
    `)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordUsage(ctx, "mac-a", "openai", "model", "account", 5, 2); err != nil {
		t.Fatal(err)
	}
	// Every startup repeats migration detection; attributed data must survive it.
	if err := store.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Usage(ctx, time.Time{})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%#v err=%v", rows, err)
	}
	for _, row := range rows {
		if row.DeviceID == "" && (row.Requests != 2 || row.CachedInputTokens != 7 || row.CacheWriteInputTokens != 3 || row.ReasoningOutputTokens != 2 || row.Routing != UsageRoutingNative) {
			t.Fatalf("legacy counters lost: %#v", row)
		}
	}
	metadata, err := store.UsageReconciliation(ctx)
	if err != nil || metadata.DeviceID != "" || metadata.EventsImported != 2 {
		t.Fatalf("legacy metadata=%#v err=%v", metadata, err)
	}
}
