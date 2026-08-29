package telemetry

import (
	"sort"
	"time"

	"github.com/opencdx/opencdx/internal/storage"
)

type ActivityPoint struct {
	Date     string `json:"date"`
	Requests int64  `json:"requests"`
}

type UsagePoint struct {
	Date                  string `json:"date"`
	Provider              string `json:"provider"`
	Model                 string `json:"model"`
	Requests              int64  `json:"requests"`
	InputTokens           int64  `json:"input_tokens"`
	CachedInputTokens     int64  `json:"cached_input_tokens"`
	CacheWriteInputTokens int64  `json:"cache_write_input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	ReasoningOutputTokens int64  `json:"reasoning_output_tokens"`
}

type Report struct {
	GeneratedAt       string          `json:"generated_at"`
	TotalRequests     int64           `json:"total_requests"`
	TotalInputTokens  int64           `json:"total_input_tokens"`
	TotalOutputTokens int64           `json:"total_output_tokens"`
	Activity          []ActivityPoint `json:"activity"`
	Usage             []UsagePoint    `json:"usage"`
}

func Build(aggregates []storage.UsageAggregate, now time.Time) Report {
	report := Report{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Activity:    make([]ActivityPoint, 0),
		Usage:       make([]UsagePoint, 0),
	}
	type usageKey struct{ day, provider, model string }
	combined := make(map[usageKey]storage.UsageAggregate)
	activity := make(map[string]int64)
	for _, aggregate := range aggregates {
		key := usageKey{day: aggregate.Day, provider: aggregate.Provider, model: aggregate.ModelID}
		current := combined[key]
		current.Day, current.Provider, current.ModelID = key.day, key.provider, key.model
		current.Requests += aggregate.Requests
		current.InputTokens += aggregate.InputTokens
		current.CachedInputTokens += aggregate.CachedInputTokens
		current.CacheWriteInputTokens += aggregate.CacheWriteInputTokens
		current.OutputTokens += aggregate.OutputTokens
		current.ReasoningOutputTokens += aggregate.ReasoningOutputTokens
		combined[key] = current
		activity[aggregate.Day] += aggregate.Requests
	}
	for date, requests := range activity {
		report.Activity = append(report.Activity, ActivityPoint{Date: date, Requests: requests})
	}
	sort.Slice(report.Activity, func(left, right int) bool { return report.Activity[left].Date < report.Activity[right].Date })

	for key, aggregate := range combined {
		point := UsagePoint{
			Date: key.day, Provider: key.provider, Model: key.model, Requests: aggregate.Requests,
			InputTokens: aggregate.InputTokens, CachedInputTokens: aggregate.CachedInputTokens,
			CacheWriteInputTokens: aggregate.CacheWriteInputTokens, OutputTokens: aggregate.OutputTokens,
			ReasoningOutputTokens: aggregate.ReasoningOutputTokens,
		}
		report.TotalRequests += point.Requests
		report.TotalInputTokens += point.InputTokens
		report.TotalOutputTokens += point.OutputTokens
		report.Usage = append(report.Usage, point)
	}
	sort.Slice(report.Usage, func(left, right int) bool {
		if report.Usage[left].Date != report.Usage[right].Date {
			return report.Usage[left].Date < report.Usage[right].Date
		}
		if report.Usage[left].Provider != report.Usage[right].Provider {
			return report.Usage[left].Provider < report.Usage[right].Provider
		}
		return report.Usage[left].Model < report.Usage[right].Model
	})
	return report
}
