package usagehistory

import (
	"fmt"
	"testing"
	"time"
)

func TestRateOnlyWeeklyObservations(t *testing.T) {
	when := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	snapshot := Snapshot{}
	state := scanState{provider: "openai", model: "gpt-test"}
	for _, id := range []string{"codex", "codex_bengalfox"} {
		line := []byte(fmt.Sprintf(`{"timestamp":%q,"payload":{"info":null,"rate_limits":{"limit_id":%q,"primary":{"used_percent":83,"window_minutes":10080,"resets_at":%d},"secondary":{"used_percent":8,"window_minutes":300,"resets_at":%d}}}}`, when.Format(time.RFC3339), id, when.Add(time.Hour).Unix(), when.Add(time.Hour).Unix()))
		if err := scanTokenCount(line, &state, &snapshot, map[rowKey]*Row{}, map[[32]byte]struct{}{}); err != nil {
			t.Fatal(err)
		}
		state.replaying = true
		if err := scanTokenCount(line, &state, &snapshot, map[rowKey]*Row{}, map[[32]byte]struct{}{}); err != nil {
			t.Fatal(err)
		}
		state.replaying = false
	}
	if len(snapshot.QuotaObservations) != 1 || snapshot.QuotaObservations[0].Used != 83 {
		t.Fatalf("observations: %#v", snapshot.QuotaObservations)
	}
}

func TestCompactQuotaPreservesTransitionBounds(t *testing.T) {
	var rows []QuotaObservation
	when := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		rows = append(rows, QuotaObservation{ObservedAt: when.Add(time.Duration(i) * time.Minute).Format(time.RFC3339), ResetAt: when.Add(time.Hour).Format(time.RFC3339), Used: 80})
	}
	last := QuotaObservation{ObservedAt: when.Add(10 * time.Minute).Format(time.RFC3339), ResetAt: when.Add(7 * 24 * time.Hour).Format(time.RFC3339), Used: 0}
	rows = append(rows, last, last)
	result := compactQuotaObservations(rows)
	if len(result) != 3 || result[0] != rows[0] || result[1] != rows[9] || result[2] != last {
		t.Fatalf("bounds lost: %#v", result)
	}
}
