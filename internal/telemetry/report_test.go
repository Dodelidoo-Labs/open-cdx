package telemetry

import (
	"testing"
	"time"

	"github.com/opencdx/opencdx/internal/storage"
)

func TestReportCombinesAccountsAndPreservesMeasuredTokens(t *testing.T) {
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	usage := []storage.UsageAggregate{
		{Day: "2026-08-28", Provider: "openai", ModelID: "gpt-test", AccountID: "first", Requests: 1, InputTokens: 100, OutputTokens: 20},
		{Day: "2026-08-28", Provider: "openai", ModelID: "gpt-test", AccountID: "second", Requests: 1, InputTokens: 50, OutputTokens: 10},
		{Day: "2026-08-28", Provider: "openrouter", ModelID: "openrouter/vendor/free", Requests: 1, InputTokens: 200, OutputTokens: 40},
		{Day: "2026-08-27", Provider: "ollama", ModelID: "ollama/llama", Requests: 1, InputTokens: 300, OutputTokens: 60},
	}
	report := Build(usage, now)
	if report.TotalRequests != 4 || report.TotalInputTokens != 650 || report.TotalOutputTokens != 130 {
		t.Fatalf("totals were not aggregated across every provider: %#v", report)
	}
	if len(report.Usage) != 3 || len(report.Activity) != 2 {
		t.Fatalf("account rows or activity days were not combined: usage=%#v activity=%#v", report.Usage, report.Activity)
	}
	for _, point := range report.Usage {
		if point.Provider == "openai" && point.Requests != 2 {
			t.Fatalf("native OpenAI usage was not combined: %#v", point)
		}
	}
}
