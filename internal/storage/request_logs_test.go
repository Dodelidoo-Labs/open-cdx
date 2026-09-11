package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRequestLogPaginationImportAndIsolation(t *testing.T) {
	store := testStore(t, filepath.Join(t.TempDir(), "logs.db"))
	ctx := context.Background()
	entries := []RequestLog{
		{ID: "a", StartedAt: time.Now(), Outcome: "success", Status: 200, Model: "model-a", DeviceID: "old-device"},
		{ID: "b", StartedAt: time.Now(), Outcome: "error", Status: 502, Model: "model-b"},
		{ID: "c", StartedAt: time.Now(), Outcome: "cancelled"},
	}
	if n, err := store.ImportRequestLogs(ctx, []RequestLog{entries[2], entries[1], entries[0]}); err != nil || n != 3 {
		t.Fatalf("import: %d %v", n, err)
	}
	if n, err := store.ImportRequestLogs(ctx, entries); err != nil || n != 0 {
		t.Fatalf("duplicate import: %d %v", n, err)
	}
	page, err := store.RequestLogs(ctx, RequestLogFilter{Limit: 2})
	if err != nil || len(page.Logs) != 2 || page.Logs[0].ID != "c" || page.NextBefore == 0 {
		t.Fatalf("page: %#v %v", page, err)
	}
	// New completions must not shift older pages.
	if err = store.RecordRequestLog(ctx, RequestLog{ID: "d", StartedAt: time.Now(), Outcome: "success", Status: 200}); err != nil {
		t.Fatal(err)
	}
	older, err := store.RequestLogs(ctx, RequestLogFilter{Limit: 2, Before: page.NextBefore})
	if err != nil || len(older.Logs) != 1 || older.Logs[0].ID != "a" || older.NextBefore != 0 {
		t.Fatalf("older: %#v %v", older, err)
	}
	filtered, err := store.RequestLogs(ctx, RequestLogFilter{Outcome: "error", Model: "model-b"})
	if err != nil || len(filtered.Logs) != 1 || filtered.Logs[0].ID != "b" {
		t.Fatalf("filtered: %#v %v", filtered, err)
	}
	usage, err := store.Usage(ctx, time.Time{})
	if err != nil || len(usage) != 0 {
		t.Fatalf("logs changed telemetry: %#v %v", usage, err)
	}
	entries[0].ID = "new"
	entries[1].Outcome = "invalid"
	if _, err = store.ImportRequestLogs(ctx, entries); err == nil {
		t.Fatal("invalid batch accepted")
	}
	if _, err = store.RequestLog(ctx, "new"); err != ErrNotFound {
		t.Fatalf("partial invalid import: %v", err)
	}
	if _, err = store.RequestLog(ctx, "a"); err != nil {
		t.Fatalf("log requires device/account to exist: %v", err)
	}
}
