package telemetry

import (
	"sort"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

type ActivityPoint struct {
	Date     string `json:"date"`
	Requests int64  `json:"requests"`
}

type UsagePoint struct {
	At                    string `json:"at,omitempty"`
	DeviceID              string `json:"device_id"`
	DeviceName            string `json:"device_name"`
	Date                  string `json:"date"`
	Provider              string `json:"provider"`
	Model                 string `json:"model"`
	Source                string `json:"source"`
	Routing               string `json:"routing"`
	Requests              int64  `json:"requests"`
	InputTokens           int64  `json:"input_tokens"`
	CachedInputTokens     int64  `json:"cached_input_tokens"`
	CacheWriteInputTokens int64  `json:"cache_write_input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	ReasoningOutputTokens int64  `json:"reasoning_output_tokens"`
}

type Reconciliation struct {
	DeviceID       string `json:"device_id"`
	ReconciledAt   string `json:"reconciled_at"`
	FilesScanned   int    `json:"files_scanned"`
	EventsImported int    `json:"events_imported"`
	RowsImported   int    `json:"rows_imported"`
}

type Report struct {
	// HourlyUsage holds every timestamped row in UTC hours; the dashboard
	// buckets it for any zoom level. UntimedUsage holds legacy daily rows.
	HourlyUsage       []UsagePoint      `json:"hourly_usage"`
	AllowanceHistory  []AllowanceSeries `json:"allowance_history"`
	AllowanceResets   []AllowanceReset  `json:"allowance_resets"`
	TimeZone          string            `json:"time_zone"`
	NextChangeAt      time.Time         `json:"-"`
	UntimedUsage      []UsagePoint      `json:"untimed_usage"`
	GeneratedAt       string            `json:"generated_at"`
	TotalRequests     int64             `json:"total_requests"`
	TotalInputTokens  int64             `json:"total_input_tokens"`
	TotalOutputTokens int64             `json:"total_output_tokens"`
	Activity          []ActivityPoint   `json:"activity"`
	Usage             []UsagePoint      `json:"usage"`
	Reconciliation    *Reconciliation   `json:"reconciliation,omitempty"`
}

func Build(aggregates []storage.UsageAggregate, reconciliation *storage.UsageReconciliation, now time.Time, locations ...*time.Location) Report {
	location := time.UTC
	if len(locations) > 0 && locations[0] != nil {
		location = locations[0]
	}
	report := Report{
		HourlyUsage: make([]UsagePoint, 0), TimeZone: location.String(), UntimedUsage: make([]UsagePoint, 0),
		GeneratedAt: now.UTC().Format(time.RFC3339Nano),
		Activity:    make([]ActivityPoint, 0),
		Usage:       make([]UsagePoint, 0),
	}
	localNow := now.In(location)
	report.NextChangeAt = time.Date(localNow.Year(), localNow.Month(), localNow.Day()+1, 0, 0, 0, 0, location)
	if reconciliation != nil {
		report.Reconciliation = &Reconciliation{
			DeviceID: reconciliation.DeviceID, ReconciledAt: reconciliation.ReconciledAt.UTC().Format(time.RFC3339),
			FilesScanned: reconciliation.FilesScanned, EventsImported: reconciliation.EventsImported,
			RowsImported: reconciliation.RowsImported,
		}
	}
	type usageKey struct{ day, provider, model, source, routing, device string }
	combined := make(map[usageKey]storage.UsageAggregate)
	activity := make(map[string]int64)
	hourly := make(map[usageKey]UsagePoint)
	for _, aggregate := range aggregates {
		recorded, err := time.Parse(time.RFC3339Nano, aggregate.RecordedAt)
		precise := err == nil
		if precise {
			aggregate.Day = recorded.In(location).Format("2006-01-02")
		}
		detail := UsagePoint{DeviceID: aggregate.DeviceID, DeviceName: aggregate.DeviceName,
			Date: aggregate.Day, Provider: aggregate.Provider, Model: aggregate.ModelID, Source: aggregate.Source, Routing: aggregate.Routing,
			Requests: aggregate.Requests, InputTokens: aggregate.InputTokens, CachedInputTokens: aggregate.CachedInputTokens,
			CacheWriteInputTokens: aggregate.CacheWriteInputTokens, OutputTokens: aggregate.OutputTokens, ReasoningOutputTokens: aggregate.ReasoningOutputTokens}
		if precise && recorded.After(now) {
			// A row from the future appears once its time arrives.
			if recorded.Before(report.NextChangeAt) {
				report.NextChangeAt = recorded
			}
		} else if precise {
			hour := recorded.UTC().Truncate(time.Hour).Format(time.RFC3339)
			key := usageKey{hour, aggregate.Provider, aggregate.ModelID, aggregate.Source, aggregate.Routing, aggregate.DeviceID}
			current, exists := hourly[key]
			if !exists {
				current = detail
				current.At = hour
			} else {
				current.Requests += detail.Requests
				current.InputTokens += detail.InputTokens
				current.OutputTokens += detail.OutputTokens
				current.CachedInputTokens += detail.CachedInputTokens
				current.CacheWriteInputTokens += detail.CacheWriteInputTokens
				current.ReasoningOutputTokens += detail.ReasoningOutputTokens
			}
			hourly[key] = current
		}
		if !precise {
			report.UntimedUsage = append(report.UntimedUsage, detail)
		}

		key := usageKey{
			device: aggregate.DeviceID, day: aggregate.Day, provider: aggregate.Provider, model: aggregate.ModelID,
			source: aggregate.Source, routing: aggregate.Routing,
		}
		current := combined[key]
		current.DeviceID, current.DeviceName = aggregate.DeviceID, aggregate.DeviceName
		current.Day, current.Provider, current.ModelID = key.day, key.provider, key.model
		current.Source, current.Routing = key.source, key.routing
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
			DeviceID: key.device, DeviceName: aggregate.DeviceName, Date: key.day, Provider: key.provider, Model: key.model, Source: key.source, Routing: key.routing,
			Requests:    aggregate.Requests,
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
		if report.Usage[left].Model != report.Usage[right].Model {
			return report.Usage[left].Model < report.Usage[right].Model
		}
		if report.Usage[left].Source != report.Usage[right].Source {
			return report.Usage[left].Source < report.Usage[right].Source
		}
		if report.Usage[left].Routing != report.Usage[right].Routing {
			return report.Usage[left].Routing < report.Usage[right].Routing
		}
		return report.Usage[left].DeviceID < report.Usage[right].DeviceID
	})
	for _, point := range hourly {
		report.HourlyUsage = append(report.HourlyUsage, point)
	}
	sort.Slice(report.HourlyUsage, func(i, j int) bool {
		a, b := report.HourlyUsage[i], report.HourlyUsage[j]
		return a.At+"\x00"+a.DeviceID+"\x00"+a.Provider+"\x00"+a.Model+"\x00"+a.Source+"\x00"+a.Routing < b.At+"\x00"+b.DeviceID+"\x00"+b.Provider+"\x00"+b.Model+"\x00"+b.Source+"\x00"+b.Routing
	})
	return report
}
