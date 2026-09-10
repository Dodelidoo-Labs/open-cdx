package telemetry

import (
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func TestReportCombinesAccountsAndPreservesMeasuredTokens(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	usage := []storage.UsageAggregate{
		{Day: "2026-08-28", Provider: "openai", ModelID: "gpt-test", AccountID: "first", Source: storage.UsageSourceRouted, Routing: storage.UsageRoutingRouted, Requests: 1, InputTokens: 100, OutputTokens: 20},
		{Day: "2026-08-28", Provider: "openai", ModelID: "gpt-test", AccountID: "second", Source: storage.UsageSourceRouted, Routing: storage.UsageRoutingRouted, Requests: 1, InputTokens: 50, OutputTokens: 10},
		{Day: "2026-08-28", Provider: "openrouter", ModelID: "openrouter/vendor/free", Source: storage.UsageSourceRouted, Routing: storage.UsageRoutingRouted, Requests: 1, InputTokens: 200, OutputTokens: 40},
		{Day: "2026-08-27", Provider: "ollama", ModelID: "ollama/llama", Source: storage.UsageSourceReconciled, Routing: storage.UsageRoutingNative, Requests: 1, InputTokens: 300, OutputTokens: 60},
	}
	reconciledAt := now.Add(-time.Hour)
	report := Build(usage, &storage.UsageReconciliation{ReconciledAt: reconciledAt, FilesScanned: 2, EventsImported: 1, RowsImported: 1}, now)
	if report.TotalRequests != 4 || report.TotalInputTokens != 650 || report.TotalOutputTokens != 130 {
		t.Fatalf("totals were not aggregated across every provider: %#v", report)
	}
	if len(report.Usage) != 3 || len(report.Activity) != 2 {
		t.Fatalf("account rows or activity days were not combined: usage=%#v activity=%#v", report.Usage, report.Activity)
	}
	for _, point := range report.Usage {
		if point.Provider == "openai" && (point.Requests != 2 || point.Source != storage.UsageSourceRouted || point.Routing != storage.UsageRoutingRouted) {
			t.Fatalf("routed OpenAI usage was not combined: %#v", point)
		}
	}
	if report.Usage[0].Routing != storage.UsageRoutingNative {
		t.Fatalf("native routing classification was not preserved: %#v", report.Usage)
	}
	if report.Reconciliation == nil || report.Reconciliation.ReconciledAt != reconciledAt.Format(time.RFC3339) || report.Reconciliation.FilesScanned != 2 {
		t.Fatalf("reconciliation boundary was not exposed: %#v", report.Reconciliation)
	}
}

func TestReportKeepsMachinesSeparateWhileCombiningTheirAccounts(t *testing.T) {
	usage := []storage.UsageAggregate{
		{DeviceID: "a", DeviceName: "Mac", Day: "2026-09-01", Provider: "openai", ModelID: "model", AccountID: "one", Requests: 1, InputTokens: 10},
		{DeviceID: "a", DeviceName: "Mac", Day: "2026-09-01", Provider: "openai", ModelID: "model", AccountID: "two", Requests: 2, InputTokens: 20},
		{DeviceID: "b", DeviceName: "Mac", Day: "2026-09-01", Provider: "openai", ModelID: "model", AccountID: "one", Requests: 4, InputTokens: 40},
		{Day: "2026-09-01", Provider: "openai", ModelID: "model", Requests: 8, InputTokens: 80},
	}
	report := Build(usage, nil, time.Now())
	if len(report.Usage) != 3 || report.TotalRequests != 15 || report.TotalInputTokens != 150 || report.Activity[0].Requests != 15 {
		t.Fatalf("report=%#v", report)
	}
	for _, point := range report.Usage {
		if point.DeviceID == "a" && (point.Requests != 3 || point.DeviceName != "Mac") {
			t.Fatalf("machine A=%#v", point)
		}
		if point.DeviceID == "b" && point.Requests != 4 {
			t.Fatalf("machine B=%#v", point)
		}
	}
}

func TestReportPreservesInstantsAndUsesConfiguredCalendarDays(t *testing.T) {
	location, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 1, 0, 0, 0, time.UTC)
	rows := []storage.UsageAggregate{
		{Day: "2026-09-09", RecordedAt: "2026-09-09T00:30:00Z", ModelID: "astra", InputTokens: 35000000, Requests: 1},
		{Day: "2026-09-08", RecordedAt: "2026-09-08T16:00:00Z", ModelID: "astra", InputTokens: 365000000, Requests: 1},
		{Day: "2026-09-01", ModelID: "astra", InputTokens: 10, Requests: 1},
	}
	report := Build(rows, nil, now, location)
	if report.TimeZone != location.String() || len(report.RollingUsage["24"]) != 1 || len(report.UntimedUsage) != 1 {
		t.Fatalf("precision lost: %#v", report)
	}
	if len(report.Usage) != 2 || report.Usage[1].Date != "2026-09-08" || report.Usage[1].InputTokens != 400000000 {
		t.Fatalf("wrong day totals: %#v", report.Usage)
	}
	if report.TotalInputTokens != 400000010 || report.RollingUsage["24"][0].InputTokens != 400000000 {
		t.Fatalf("totals or instant lost: %#v", report)
	}
	utc := Build(rows, nil, now)
	if utc.TimeZone != "UTC" || len(utc.Usage) != 3 {
		t.Fatalf("default used host timezone: %#v", utc)
	}
}

func TestRollingUsageCutoffsAndExpiryAreIndependentOfMidnight(t *testing.T) {
	now := time.Date(2026, 9, 9, 1, 0, 0, 0, time.UTC)
	boundary := now.Add(-24 * time.Hour)
	rows := []storage.UsageAggregate{}
	for _, at := range []time.Time{boundary.Add(-time.Nanosecond), boundary, now, now.Add(time.Second)} {
		rows = append(rows, storage.UsageAggregate{RecordedAt: at.Format(time.RFC3339Nano), Day: at.Format("2006-01-02"), InputTokens: 10, Requests: 1})
	}
	sum := func(report Report, hours string) int64 {
		var n int64
		for _, row := range report.RollingUsage[hours] {
			n += row.InputTokens
		}
		return n
	}
	initial := Build(rows, nil, now)
	if sum(initial, "24") != 20 || sum(initial, "168") != 30 || !initial.NextChangeAt.Equal(now.Add(time.Nanosecond)) {
		t.Fatalf("wrong cutoff/expiry: %#v", initial)
	}
	after := Build(rows, nil, initial.NextChangeAt)
	if sum(after, "24") != 10 {
		t.Fatalf("expired usage remained: %#v", after.RollingUsage)
	}
	future := Build(rows, nil, now.Add(time.Second))
	if sum(future, "24") != 20 {
		t.Fatalf("future response missing: %#v", future.RollingUsage)
	}
}
