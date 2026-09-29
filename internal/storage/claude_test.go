package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeRequestsCountOnceAcrossSources(t *testing.T) {
	store := testStore(t, filepath.Join(t.TempDir(), "claude.db"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	account, err := store.EnsureClaudeAccount(ctx, "identity", "s***e@e***.com", "device-a")
	if err != nil {
		t.Fatal(err)
	}
	live := []ClaudeRequest{
		{RequestID: "req_1", RecordedAt: at, Model: "claude-opus-5-5", AccountID: account, InputTokens: 1110, CachedInputTokens: 1000, CacheWriteInputTokens: 100, OutputTokens: 20},
		{RequestID: "req_2", RecordedAt: at, Model: "claude-opus-5-5", AccountID: account, InputTokens: 5, OutputTokens: 1},
	}
	if added, err := store.RecordClaudeRequests(ctx, "device-a", UsageSourceRouted, live); err != nil || added != 2 {
		t.Fatalf("live added = %d, %v", added, err)
	}
	history := []ClaudeRequest{live[0], {RequestID: "req_3", RecordedAt: at.Add(-24 * time.Hour), Model: "claude-haiku-4-5", InputTokens: 7, OutputTokens: 3, ReasoningOutputTokens: 2}}
	if added, err := store.RecordClaudeRequests(ctx, "device-b", UsageSourceReconciled, history); err != nil || added != 1 {
		t.Fatalf("history added = %d, %v", added, err)
	}
	usage, err := store.Usage(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var requests, input, cached, written, output, reasoning int64
	for _, row := range usage {
		if row.Provider != ProviderClaudeCode || row.Routing != UsageRoutingNative {
			t.Fatalf("row = %#v", row)
		}
		requests += row.Requests
		input, cached, written, output, reasoning = input+row.InputTokens, cached+row.CachedInputTokens, written+row.CacheWriteInputTokens, output+row.OutputTokens, reasoning+row.ReasoningOutputTokens
	}
	if requests != 3 || input != 1122 || cached != 1000 || written != 100 || output != 24 || reasoning != 2 {
		t.Fatalf("totals = %d %d %d %d %d %d", requests, input, cached, written, output, reasoning)
	}

	// A Codex import replaces that machine's Codex rows, never Claude Code rows.
	if err = store.ReplaceUsage(ctx, "device-a", []UsageAggregate{{Day: at.Format("2006-01-02"), Provider: "openai", ModelID: "gpt-5", Routing: UsageRoutingNative, Requests: 1}}, UsageReconciliation{ReconciledAt: at}); err != nil {
		t.Fatal(err)
	}
	// Two live requests share a timestamp and therefore one aggregate row.
	if usage, _ = store.Usage(ctx, time.Time{}); len(usage) != 3 {
		t.Fatalf("usage after Codex import = %#v", usage)
	}
	if err = store.ResetTelemetry(ctx); err != nil {
		t.Fatal(err)
	}
	if added, err := store.RecordClaudeRequests(ctx, "device-b", UsageSourceReconciled, history); err != nil || added != 2 {
		t.Fatalf("import after reset added = %d, %v", added, err)
	}
	if _, err = store.RecordClaudeRequests(ctx, "device-a", "other", live); err == nil {
		t.Fatal("invalid source was accepted")
	}
}

func TestClaudeAllowanceSnapshotsAndThrottle(t *testing.T) {
	store := testStore(t, filepath.Join(t.TempDir(), "claude.db"))
	ctx := context.Background()
	first, err := store.EnsureClaudeAccount(ctx, "identity", "", "device-a")
	if err != nil {
		t.Fatal(err)
	}
	same, err := store.EnsureClaudeAccount(ctx, "identity", "s***e@e***.com", "device-b")
	if err != nil || same != first {
		t.Fatalf("account identity was not stable: %q %q %v", first, same, err)
	}
	if _, err = store.EnsureClaudeAccount(ctx, "", "", "device-a"); err == nil {
		t.Fatal("empty identity was accepted")
	}
	now := time.Now().UTC().Truncate(time.Second)
	windows := []ClaudeWindow{{WindowSeconds: 604800, UsedPercent: 64, ResetAt: now.Add(72 * time.Hour)}, {WindowSeconds: 18000, UsedPercent: 5, ResetAt: now.Add(2 * time.Hour)}}
	record := func(at time.Time, values []ClaudeWindow) {
		t.Helper()
		if err := store.RecordClaudeAllowance(ctx, first, at, values); err != nil {
			t.Fatal(err)
		}
	}
	record(now.Add(-10*time.Minute), windows)
	record(now.Add(-9*time.Minute), windows) // identical within five minutes
	record(now.Add(-4*time.Minute), windows) // identical, but a periodic reading
	changed := []ClaudeWindow{windows[0], {WindowSeconds: 18000, UsedPercent: 9, ResetAt: windows[1].ResetAt}}
	record(now.Add(-3*time.Minute), changed)
	record(now.Add(-20*time.Minute), windows) // late delivery keeps history but not the snapshot
	observations, err := store.AllowanceObservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 7 {
		t.Fatalf("observations = %d %#v", len(observations), observations)
	}
	for _, o := range observations {
		if o.Provider != ProviderClaudeCode || o.Label != "Claude · s***e@e***.com" || o.Source != "live" || o.AccountID != first {
			t.Fatalf("observation = %#v", o)
		}
	}
	accounts, err := store.ClaudeAccounts(ctx)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts = %#v %v", accounts, err)
	}
	if accounts[0].LastDeviceID != "device-b" || !accounts[0].ObservedAt.Equal(now.Add(-3*time.Minute)) || len(accounts[0].Windows) != 2 || accounts[0].Windows[1].UsedPercent != 9 {
		t.Fatalf("snapshot = %#v", accounts[0])
	}
	if err = store.RecordClaudeAllowance(ctx, "missing", now, windows); err != ErrNotFound {
		t.Fatalf("unknown account error = %v", err)
	}
	if err = store.RecordClaudeAllowance(ctx, first, now, []ClaudeWindow{{WindowSeconds: 18000, UsedPercent: 5, ResetAt: now.Add(48 * time.Hour)}}); err == nil {
		t.Fatal("reset beyond the window was accepted")
	}
	if err = store.ResetTelemetry(ctx); err != nil {
		t.Fatal(err)
	}
	if accounts, _ = store.ClaudeAccounts(ctx); len(accounts) != 1 || len(accounts[0].Windows) != 2 {
		t.Fatal("telemetry reset removed the current Claude allowance")
	}
}
