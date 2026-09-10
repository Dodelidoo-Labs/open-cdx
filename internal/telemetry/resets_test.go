package telemetry

import (
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
	"testing"
	"time"
)

func TestAllowanceTransitionsAndCycleAttribution(t *testing.T) {
	start := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	observation := func(at, reset time.Time, used float64) storage.AllowanceObservation {
		return storage.AllowanceObservation{Source: "history", DeviceID: "a", ObservedAt: at, ResetAt: reset, Used: used}
	}
	next := start.Add(7 * 24 * time.Hour)
	early := start.Add(24 * time.Hour)
	rows := []storage.AllowanceObservation{
		observation(start.Add(-time.Hour), start, 83),
		observation(start.Add(time.Second), next, 2),                    // zero need not have been sampled
		observation(start.Add(time.Hour), next.Add(25*time.Second), 40), // deadline jitter
		observation(start.Add(2*time.Hour), next, 39),                   // percentage correction alone
		observation(early, next.Add(24*time.Hour), 0),                   // early reset: bounded inference
		observation(early.Add(time.Hour), next, 40),                     // old window rebroadcast
	}
	mkUsage := func(at time.Time, device, account, source string, tokens int64) storage.UsageAggregate {
		return storage.UsageAggregate{RecordedAt: at.Format(time.RFC3339Nano), Provider: "openai", DeviceID: device, AccountID: account, Source: source, InputTokens: tokens, OutputTokens: 10, CachedInputTokens: tokens, Requests: 1}
	}
	usage := []storage.UsageAggregate{
		mkUsage(start.Add(-time.Second), "a", "", "reconciled", 500),
		mkUsage(start, "a", "", "reconciled", 100),
		mkUsage(early, "a", "", "reconciled", 200),
		mkUsage(early, "b", "", "reconciled", 900),
		mkUsage(early, "a", "account", "routed", 300),
	}
	now := early.Add(2 * time.Hour)
	// Independent account stream, including a baseline that must not be a reset.
	rows = append(rows, storage.AllowanceObservation{Source: "live", AccountID: "account", ObservedAt: start.Add(-time.Hour), ResetAt: start, Used: 90}, storage.AllowanceObservation{Source: "live", AccountID: "account", ObservedAt: start.Add(time.Second), ResetAt: next, Used: 0})
	resets := BuildAllowanceResets(rows, usage, now)
	if len(resets) != 3 {
		t.Fatalf("resets = %#v", resets)
	}
	var history []AllowanceReset
	for _, r := range resets {
		if r.Source == "history" {
			history = append(history, r)
		} else if len(r.Usage) != 1 || r.Usage[0].Tokens != 310 {
			t.Fatalf("account attribution: %#v", r)
		}
	}
	if !history[0].Scheduled || !history[0].At.Equal(start) || history[0].ObservedRemaining != 98 || history[0].Until == nil || !history[0].Until.Equal(early) || history[0].Usage[0].Tokens != 110 {
		t.Fatalf("scheduled cycle: %#v", history[0])
	}
	if history[1].Scheduled || !history[1].After.Equal(start.Add(2*time.Hour)) || history[1].Usage[0].Tokens != 520 || history[1].Until != nil {
		t.Fatalf("early cycle: %#v", history[1])
	}
}

func TestAllowanceNoInventedResetsOrLegacyTotals(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	base := storage.AllowanceObservation{Source: "history", DeviceID: "a", ObservedAt: now.Add(-48 * time.Hour), ResetAt: now.Add(-24 * time.Hour), Used: 90}
	if got := BuildAllowanceResets([]storage.AllowanceObservation{base}, nil, now); len(got) != 0 {
		t.Fatal("expired deadline is not proof of reset")
	}
	next := base
	next.ObservedAt = now.Add(-23 * time.Hour)
	next.ResetAt = base.ResetAt.Add(7 * 24 * time.Hour)
	next.Used = 0
	other := next
	other.Source = "live"
	other.AccountID = "other"
	other.DeviceID = ""
	got := BuildAllowanceResets([]storage.AllowanceObservation{next, base, next, other}, []storage.UsageAggregate{{DeviceID: "a", Provider: "openai", Day: "2026-08-27", InputTokens: 100000}}, now)
	if len(got) != 1 || len(got[0].Usage) != 1 || !got[0].Usage[0].Untimed || got[0].Usage[0].Tokens != 0 {
		t.Fatalf("legacy/dedup/baseline: %#v", got)
	}
}
