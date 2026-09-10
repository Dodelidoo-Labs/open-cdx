package usagehistory

import (
	"math"
	"sort"
	"time"
)

// QuotaObservation contains only the main Codex weekly allowance measurement.
// Rollouts do not reliably identify the billed account; never infer one here.
type QuotaObservation struct {
	ObservedAt string  `json:"observed_at"`
	ResetAt    string  `json:"reset_at"`
	Used       float64 `json:"used_percent"`
}

type rolloutRateLimits struct {
	LimitID   string              `json:"limit_id"`
	Primary   *rolloutQuotaWindow `json:"primary"`
	Secondary *rolloutQuotaWindow `json:"secondary"`
}

type rolloutQuotaWindow struct {
	Minutes int      `json:"window_minutes"`
	Used    *float64 `json:"used_percent"`
	Reset   int64    `json:"resets_at"`
}

func collectQuota(snapshot *Snapshot, limits *rolloutRateLimits, when time.Time) {
	if limits == nil || (limits.LimitID != "" && limits.LimitID != "codex") {
		return
	}
	for _, window := range []*rolloutQuotaWindow{limits.Primary, limits.Secondary} {
		if window == nil || window.Minutes != 10080 || window.Used == nil || window.Reset <= 0 {
			continue
		}
		used := *window.Used
		reset := time.Unix(window.Reset, 0).UTC()
		if math.IsNaN(used) || math.IsInf(used, 0) || used < 0 || used > 100 || reset.Add(5*time.Minute).Before(when) || reset.After(when.Add(7*24*time.Hour+5*time.Minute)) {
			continue
		}
		snapshot.QuotaObservations = append(snapshot.QuotaObservations, QuotaObservation{ObservedAt: when.UTC().Format(time.RFC3339Nano), ResetAt: reset.Format(time.RFC3339), Used: used})
	}
}

// Keep the first and last measurement of an unchanged state. This preserves
// transition bounds without uploading every rebroadcast in a long conversation.
func compactQuotaObservations(rows []QuotaObservation) []QuotaObservation {
	sort.Slice(rows, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, rows[i].ObservedAt)
		b, _ := time.Parse(time.RFC3339Nano, rows[j].ObservedAt)
		if !a.Equal(b) {
			return a.Before(b)
		}
		if rows[i].ResetAt != rows[j].ResetAt {
			return rows[i].ResetAt < rows[j].ResetAt
		}
		return rows[i].Used < rows[j].Used
	})
	result := make([]QuotaObservation, 0)
	same := func(a, b QuotaObservation) bool {
		x, _ := time.Parse(time.RFC3339, a.ResetAt)
		y, _ := time.Parse(time.RFC3339, b.ResetAt)
		return a.Used == b.Used && x.Sub(y).Abs() <= 30*time.Second
	}
	for _, row := range rows {
		n := len(result)
		if n > 0 && result[n-1] == row {
			continue
		}
		if n > 1 && same(result[n-1], row) && same(result[n-2], row) {
			result[n-1] = row
		} else {
			result = append(result, row)
		}
	}
	return result
}
