package telemetry

import (
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
	"testing"
	"time"
)

func TestAllowanceHistoryUsesOnlyAccountReadingsAndKeepsWindowIdentity(t *testing.T) {
	now := time.Now().UTC()
	base := storage.AllowanceObservation{Source: "live", AccountID: "a", Label: "masked", ObservedAt: now.Add(-time.Hour), ResetAt: now.Add(time.Hour), Used: 35}
	rows := []storage.AllowanceObservation{base}
	short := base
	short.WindowSeconds = 18000
	short.Used = 90
	rows = append(rows, short)
	history := base
	history.Source = "history"
	history.AccountID = ""
	history.DeviceID = "device"
	rows = append(rows, history)
	invalid := base
	invalid.Used = 101
	rows = append(rows, invalid)
	future := base
	future.ObservedAt = now.Add(time.Minute)
	rows = append(rows, future)
	newer := base
	newer.ObservedAt = now
	newer.Used = 40
	rows = append([]storage.AllowanceObservation{newer}, rows...)
	got := BuildAllowanceHistory(rows, now)
	if len(got) != 2 || got[0].WindowSeconds != 604800 || got[1].WindowSeconds != 18000 {
		t.Fatalf("window identity: %#v", got)
	}
	if len(got[0].Points) != 2 || got[0].Points[0].Remaining != 65 || got[0].Points[1].Remaining != 60 || got[1].Points[0].Remaining != 10 {
		t.Fatalf("readings changed or leaked: %#v", got)
	}
	// Short-window transitions must never be mistaken for weekly reset markers.
	shortNext := short
	shortNext.ObservedAt = now
	shortNext.ResetAt = now.Add(5 * time.Hour)
	shortNext.Used = 0
	if resets := BuildAllowanceResets([]storage.AllowanceObservation{short, shortNext}, nil, now); len(resets) != 0 {
		t.Fatal(resets)
	}
}

func TestHourlyUsageMatchesExactRollingWindowAndPreservesMachineScope(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 30, 0, 0, time.UTC)
	rows := []storage.UsageAggregate{}
	for i, at := range []time.Time{now.Add(-24*time.Hour - time.Second), now.Add(-24 * time.Hour), now.Add(-23 * time.Hour), now, now.Add(time.Second)} {
		rows = append(rows, storage.UsageAggregate{RecordedAt: at.Format(time.RFC3339Nano), DeviceID: []string{"a", "b"}[i%2], Provider: "openai", ModelID: "model", Requests: 1, InputTokens: 100, OutputTokens: 10, CachedInputTokens: 50})
	}
	report := Build(rows, nil, now)
	var hourly, rolling int64
	for _, row := range report.HourlyUsage {
		hourly += row.InputTokens + row.OutputTokens
		if row.At == "" || row.DeviceID == "" {
			t.Fatal("lost hour/device")
		}
	}
	for _, row := range report.RollingUsage["24"] {
		rolling += row.InputTokens + row.OutputTokens
	}
	if hourly != 330 || hourly != rolling {
		t.Fatalf("hourly=%d rolling=%d", hourly, rolling)
	}
}
