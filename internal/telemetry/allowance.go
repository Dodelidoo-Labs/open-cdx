package telemetry

import (
	"sort"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

type AllowancePoint struct {
	At        time.Time `json:"at"`
	ResetAt   time.Time `json:"reset_at"`
	Remaining float64   `json:"remaining"`
}

type AllowanceSeries struct {
	AccountID     string           `json:"account_id"`
	Label         string           `json:"label"`
	WindowSeconds int64            `json:"window_seconds"`
	WindowLabel   string           `json:"window_label"`
	Points        []AllowancePoint `json:"points"`
}

// Imported rollout history cannot identify the billed account. Only live,
// account-attributed readings belong on the allowance overlay.
func BuildAllowanceHistory(observations []storage.AllowanceObservation, now time.Time) []AllowanceSeries {
	type key struct {
		account string
		seconds int64
	}
	streams := make(map[key]*AllowanceSeries)
	for _, o := range observations {
		if o.Source != "live" || o.AccountID == "" || o.ObservedAt.After(now) || o.Validate(now) != nil {
			continue
		}
		k := key{o.AccountID, int64(o.WindowDuration() / time.Second)}
		if streams[k] == nil {
			streams[k] = &AllowanceSeries{AccountID: o.AccountID, Label: o.Label, WindowSeconds: k.seconds, WindowLabel: (openai.QuotaWindow{Duration: o.WindowDuration()}).Label(), Points: make([]AllowancePoint, 0)}
		}
		streams[k].Points = append(streams[k].Points, AllowancePoint{At: o.ObservedAt, ResetAt: o.ResetAt, Remaining: 100 - o.Used})
	}
	result := make([]AllowanceSeries, 0, len(streams))
	for _, stream := range streams {
		sort.Slice(stream.Points, func(i, j int) bool {
			a, b := stream.Points[i], stream.Points[j]
			if !a.At.Equal(b.At) {
				return a.At.Before(b.At)
			}
			if !a.ResetAt.Equal(b.ResetAt) {
				return a.ResetAt.Before(b.ResetAt)
			}
			return a.Remaining < b.Remaining
		})
		result = append(result, *stream)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AccountID != result[j].AccountID {
			return result[i].AccountID < result[j].AccountID
		}
		return result[i].WindowSeconds > result[j].WindowSeconds
	})
	return result
}
