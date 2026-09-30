package claudecode

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// usageFixture is the result text Claude Code 2.1.285 prints for
// `claude -p /usage --output-format json` on a Max plan.
const usageFixture = "You are currently using your subscription to power your Claude Code usage\n\n" +
	"Current session: 20% used · resets Sep 30 at 4:40pm (America/Buenos_Aires)\n" +
	"Current week (all models): 69% used · resets Oct 3 at 1pm (America/Buenos_Aires)\n" +
	"Current week (Fable): 36% used · resets Oct 3 at 1pm (America/Buenos_Aires)\n\n" +
	"What's contributing to your limits usage?\n" +
	"Last 24h · 890 requests · 10 sessions\n  92% of your usage was at >150k context\n"

func usageOutput(t *testing.T, result string, isError bool) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"type": "result", "is_error": isError, "result": result, "total_cost_usd": 0})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseUsageCommandReadsSessionAndWeeklyWindows(t *testing.T) {
	now := time.Date(2026, 9, 30, 18, 10, 0, 0, time.UTC)
	windows, err := ParseUsageCommand(usageOutput(t, usageFixture, false), now)
	if err != nil {
		t.Fatal(err)
	}
	want := []Window{
		{Seconds: 7 * 24 * 3600, UsedPercent: 69, ResetAt: "2026-10-03T16:00:00Z"},
		{Seconds: 5 * 3600, UsedPercent: 20, ResetAt: "2026-09-30T19:40:00Z"},
	}
	if len(windows) != len(want) || windows[0] != want[0] || windows[1] != want[1] {
		t.Fatalf("windows = %#v, want %#v", windows, want)
	}
}

func TestParseUsageCommandReadsOtherDateForms(t *testing.T) {
	now := time.Date(2026, 12, 30, 12, 0, 0, 0, time.UTC)
	result := "Current session: 5% used · resets Dec 30, 3:15 PM (UTC)\n" +
		"Current week (all models): 12.5% used · resets Jan 2, 2027, 9am (UTC)\n"
	windows, err := ParseUsageCommand(usageOutput(t, result, false), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 || windows[0].ResetAt != "2027-01-02T09:00:00Z" || windows[0].UsedPercent != 12.5 || windows[1].ResetAt != "2026-12-30T15:15:00Z" {
		t.Fatalf("windows = %#v", windows)
	}
}

func TestParseUsageCommandDropsStaleAndUnknownWindows(t *testing.T) {
	now := time.Date(2026, 9, 30, 18, 10, 0, 0, time.UTC)
	result := "Current session: 20% used · resets Sep 30 at 1pm (UTC)\n" + // already reset
		"Current session: 3% used\n" + // no reset time
		"Current week (all models): 69% used · resets Nov 3 at 1pm (UTC)\n" + // beyond a week
		"Current week (Sonnet only): 10% used · resets Oct 3 at 1pm (UTC)\n"
	if _, err := ParseUsageCommand(usageOutput(t, result, false), now); !errors.Is(err, ErrNoUsageWindows) {
		t.Fatalf("err = %v, want ErrNoUsageWindows", err)
	}
}

func TestParseUsageCommandRejectsErrorsAndOtherOutput(t *testing.T) {
	now := time.Date(2026, 9, 30, 18, 10, 0, 0, time.UTC)
	if _, err := ParseUsageCommand(usageOutput(t, usageFixture, true), now); err == nil {
		t.Fatal("an is_error result was accepted")
	}
	if _, err := ParseUsageCommand([]byte("Current session: 20% used"), now); err == nil {
		t.Fatal("plain text was accepted")
	}
	apiKey := "You are currently using your API key to power your Claude Code usage\n"
	if _, err := ParseUsageCommand(usageOutput(t, apiKey, false), now); !errors.Is(err, ErrNoUsageWindows) {
		t.Fatalf("err = %v, want ErrNoUsageWindows", err)
	}
}
