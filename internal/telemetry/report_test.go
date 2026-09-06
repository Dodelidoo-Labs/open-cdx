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
