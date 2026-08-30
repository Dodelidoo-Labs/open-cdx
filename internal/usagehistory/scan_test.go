package usagehistory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanUsesCumulativeDeltasDeduplicatesAndNeverExportsConversationContent(t *testing.T) {
	home := t.TempDir()
	sessions := filepath.Join(home, "sessions", "2026", "08", "28")
	archived := filepath.Join(home, "archived_sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(archived, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"timestamp":"2026-08-27T23:59:50Z","type":"session_meta","payload":{"id":"session-a","model_provider":"opencdx"}}`,
		`{"timestamp":"2026-08-28T00:00:00Z","type":"turn_context","payload":{"turn_id":"turn-a","model":"openrouter/vendor/model","developer_instructions":"PRIVATE-DEVELOPER-CONTENT"}}`,
		`{"timestamp":"2026-08-28T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"PRIVATE-PROMPT-CONTENT"}]}}`,
		`{"timestamp":"2026-08-28T00:00:02Z","ordinal":1,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":40,"cache_write_input_tokens":3,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":120},"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"cache_write_input_tokens":3,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":120}},"rate_limits":null}}`,
		// Rate-limit-only rebroadcast: the cumulative counters did not advance.
		`{"timestamp":"2026-08-28T00:00:03Z","ordinal":2,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":40,"cache_write_input_tokens":3,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":120},"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"cache_write_input_tokens":3,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":120}},"rate_limits":{"primary":{"used_percent":1}}}}`,
		`{"timestamp":"2026-08-28T00:00:04Z","ordinal":3,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":180,"cached_input_tokens":50,"cache_write_input_tokens":3,"output_tokens":50,"reasoning_output_tokens":12,"total_tokens":230},"last_token_usage":{"input_tokens":80,"cached_input_tokens":10,"cache_write_input_tokens":0,"output_tokens":30,"reasoning_output_tokens":7,"total_tokens":110}}}}`,
		`{"timestamp":"2026-08-28T01:00:00Z","type":"turn_context","payload":{"turn_id":"turn-b","model":"ollama/llama3.1:8b"}}`,
		`{"timestamp":"2026-08-28T01:00:01Z","ordinal":4,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":200,"cached_input_tokens":50,"cache_write_input_tokens":3,"output_tokens":60,"reasoning_output_tokens":12,"total_tokens":260},"last_token_usage":{"input_tokens":20,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":0,"total_tokens":30}}}}`,
	}
	contents := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(sessions, "rollout-a.jsonl"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	// A fork/archive can contain copied usage lines. Exact copied events count once.
	if err := os.WriteFile(filepath.Join(archived, "rollout-copy.jsonl"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Scan(context.Background(), home, time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.FilesScanned != 2 || snapshot.EventsImported != 3 || snapshot.DuplicateEvents != 5 || len(snapshot.Rows) != 2 {
		t.Fatalf("unexpected scan summary: %#v", snapshot)
	}
	var openRouter, ollama Row
	for _, row := range snapshot.Rows {
		switch row.Provider {
		case "openrouter":
			openRouter = row
		case "ollama":
			ollama = row
		}
	}
	if openRouter.Provider != "openrouter" || openRouter.Model != "openrouter/vendor/model" || openRouter.Routing != RoutingRouted || openRouter.Requests != 2 || openRouter.InputTokens != 180 || openRouter.CachedInputTokens != 50 || openRouter.CacheWriteInputTokens != 3 || openRouter.OutputTokens != 50 || openRouter.ReasoningOutputTokens != 12 {
		t.Fatalf("OpenRouter history was counted incorrectly: %#v", openRouter)
	}
	if ollama.Provider != "ollama" || ollama.Routing != RoutingRouted || ollama.Requests != 1 || ollama.InputTokens != 20 || ollama.OutputTokens != 10 {
		t.Fatalf("Ollama history was counted incorrectly: %#v", ollama)
	}
	exported, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(exported), "PRIVATE-PROMPT-CONTENT") || strings.Contains(string(exported), "PRIVATE-DEVELOPER-CONTENT") {
		t.Fatal("conversation content escaped the privacy-minimal snapshot")
	}
}

func TestScanTracksCurrentThreadSettingsProviderAndModel(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "sessions")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	contents := strings.Join([]string{
		`{"timestamp":"2026-08-28T00:00:00Z","type":"session_meta","payload":{"model_provider":"openai"}}`,
		`{"timestamp":"2026-08-28T00:00:01Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"model":"vendor/new-model","model_provider_id":"custom-provider"}}}`,
		`{"timestamp":"2026-08-28T00:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10},"last_token_usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(directory, "rollout.jsonl"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Scan(context.Background(), home, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Rows) != 1 || snapshot.Rows[0].Provider != "custom-provider" || snapshot.Rows[0].Model != "vendor/new-model" || snapshot.Rows[0].Routing != RoutingNative {
		t.Fatalf("thread settings were not applied: %#v", snapshot.Rows)
	}
}

func TestScanPreservesRoutedAndNativeUsageForSameUpstreamModel(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "sessions")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	rollout := func(provider string) string {
		return strings.Join([]string{
			`{"timestamp":"2026-08-28T00:00:00Z","type":"session_meta","payload":{"model_provider":"` + provider + `"}}`,
			`{"timestamp":"2026-08-28T00:00:01Z","type":"turn_context","payload":{"turn_id":"turn","model":"gpt-test"}}`,
			`{"timestamp":"2026-08-28T00:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10},"last_token_usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10}}}}`,
		}, "\n") + "\n"
	}
	if err := os.WriteFile(filepath.Join(directory, "native.jsonl"), []byte(rollout("openai")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "routed.jsonl"), []byte(rollout("opencdx")), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Scan(context.Background(), home, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EventsImported != 2 || snapshot.DuplicateEvents != 0 || len(snapshot.Rows) != 2 {
		t.Fatalf("routing classifications were merged or deduplicated: %#v", snapshot)
	}
	if snapshot.Rows[0].Provider != "openai" || snapshot.Rows[0].Model != "gpt-test" ||
		snapshot.Rows[0].Routing != RoutingNative || snapshot.Rows[1].Routing != RoutingRouted {
		t.Fatalf("unexpected routing classifications: %#v", snapshot.Rows)
	}
}

func TestScanDoesNotDeduplicateIndependentTurnsWithIdenticalUsage(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "sessions")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	rollout := func(turn string) string {
		return strings.Join([]string{
			`{"timestamp":"2026-08-28T00:00:00Z","type":"session_meta","payload":{"model_provider":"openai"}}`,
			`{"timestamp":"2026-08-28T00:00:01Z","type":"turn_context","payload":{"turn_id":"` + turn + `","model":"gpt-test"}}`,
			`{"timestamp":"2026-08-28T00:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10},"last_token_usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10}}}}`,
		}, "\n") + "\n"
	}
	if err := os.WriteFile(filepath.Join(directory, "first.jsonl"), []byte(rollout("turn-first")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "second.jsonl"), []byte(rollout("turn-second")), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Scan(context.Background(), home, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EventsImported != 2 || snapshot.DuplicateEvents != 0 || len(snapshot.Rows) != 1 || snapshot.Rows[0].Requests != 2 {
		t.Fatalf("independent turns were collapsed: %#v", snapshot)
	}
}

func TestScanStreamDiscardsOversizedConversationRecords(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "sessions")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	largePrivateContent := strings.Repeat("PRIVATE-IMAGE-OR-PROMPT", maxRelevantRecordBytes/10)
	contents := strings.Join([]string{
		`{"timestamp":"2026-08-28T00:00:00Z","type":"session_meta","payload":{"model_provider":"openai"}}`,
		`{"timestamp":"2026-08-28T00:00:01Z","type":"turn_context","payload":{"turn_id":"turn","model":"gpt-test"}}`,
		`{"timestamp":"2026-08-28T00:00:02Z","type":"response_item","payload":{"type":"message","content":"` + largePrivateContent + `"}}`,
		`{"timestamp":"2026-08-28T00:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10},"last_token_usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(directory, "rollout.jsonl"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Scan(context.Background(), home, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EventsImported != 1 || len(snapshot.Rows) != 1 || snapshot.Rows[0].InputTokens != 8 {
		t.Fatalf("usage following the oversized private record was lost: %#v", snapshot)
	}
	if snapshot.MalformedLines != 0 {
		t.Fatalf("an intentionally ignored conversation record was treated as malformed: %#v", snapshot)
	}
}

func TestScanUsesCopiedChildHistoryOnlyAsCumulativeBaseline(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "sessions")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	contents := strings.Join([]string{
		`{"timestamp":"2026-08-28T12:00:00.000Z","type":"session_meta","payload":{"id":"child","parent_thread_id":"parent","model_provider":"openai"}}`,
		`{"timestamp":"2026-08-28T12:00:00.010Z","type":"turn_context","payload":{"turn_id":"copied-turn","model":"gpt-test"}}`,
		`{"timestamp":"2026-08-28T12:00:00.020Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":80,"output_tokens":20,"total_tokens":100},"last_token_usage":{"input_tokens":80,"output_tokens":20,"total_tokens":100}}}}`,
		`{"timestamp":"2026-08-28T12:00:00.030Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":160,"output_tokens":40,"total_tokens":200},"last_token_usage":{"input_tokens":80,"output_tokens":20,"total_tokens":100}}}}`,
		`{"timestamp":"2026-08-28T12:00:05.000Z","type":"turn_context","payload":{"turn_id":"real-turn","model":"gpt-test"}}`,
		`{"timestamp":"2026-08-28T12:00:05.100Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":184,"output_tokens":46,"total_tokens":230},"last_token_usage":{"input_tokens":24,"output_tokens":6,"total_tokens":30}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(directory, "child.jsonl"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Scan(context.Background(), home, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EventsImported != 1 || len(snapshot.Rows) != 1 || snapshot.Rows[0].InputTokens != 24 || snapshot.Rows[0].OutputTokens != 6 {
		t.Fatalf("copied child history was counted instead of used as a baseline: %#v", snapshot)
	}
}

func TestScanDeduplicatesCopiedCumulativeHistoryWithRewrittenTimestamps(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "sessions")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	original := strings.Join([]string{
		`{"timestamp":"2026-08-27T12:00:00Z","type":"session_meta","payload":{"id":"original","model_provider":"openai"}}`,
		`{"timestamp":"2026-08-27T12:00:01Z","type":"turn_context","payload":{"turn_id":"turn-a","model":"gpt-test"}}`,
		`{"timestamp":"2026-08-27T12:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":80,"output_tokens":20,"total_tokens":120},"last_token_usage":{"input_tokens":100,"cached_input_tokens":80,"output_tokens":20,"total_tokens":120}}}}`,
		`{"timestamp":"2026-08-27T12:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":180,"cached_input_tokens":140,"output_tokens":30,"total_tokens":210},"last_token_usage":{"input_tokens":80,"cached_input_tokens":60,"output_tokens":10,"total_tokens":90}}}}`,
	}, "\n") + "\n"
	copyWithNewTimestamps := strings.ReplaceAll(original, "2026-08-27T12:00:", "2026-08-28T09:30:")
	copyWithNewTimestamps = strings.Replace(copyWithNewTimestamps, `"id":"original"`, `"id":"resumed-copy"`, 1)
	if err := os.WriteFile(filepath.Join(directory, "original.jsonl"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "resumed.jsonl"), []byte(copyWithNewTimestamps), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Scan(context.Background(), home, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EventsImported != 2 || snapshot.DuplicateEvents != 2 || len(snapshot.Rows) != 1 {
		t.Fatalf("rewritten copied history was not deduplicated: %#v", snapshot)
	}
	row := snapshot.Rows[0]
	if row.Requests != 2 || row.InputTokens != 180 || row.CachedInputTokens != 140 || row.OutputTokens != 30 {
		t.Fatalf("copied usage was counted more than once: %#v", row)
	}
}

func TestScanUsesLastUsageWhenCumulativeComponentsAreRebased(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "sessions")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	contents := strings.Join([]string{
		`{"timestamp":"2026-08-28T12:00:00Z","type":"session_meta","payload":{"model_provider":"openai"}}`,
		`{"timestamp":"2026-08-28T12:00:01Z","type":"turn_context","payload":{"turn_id":"turn","model":"gpt-test"}}`,
		`{"timestamp":"2026-08-28T12:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":900,"output_tokens":100,"total_tokens":1100},"last_token_usage":{"input_tokens":100,"cached_input_tokens":80,"output_tokens":10,"total_tokens":110}}}}`,
		`{"timestamp":"2026-08-28T12:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1100,"cached_input_tokens":200,"output_tokens":110,"total_tokens":1210},"last_token_usage":{"input_tokens":100,"cached_input_tokens":50,"output_tokens":10,"total_tokens":110}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(directory, "rebased.jsonl"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Scan(context.Background(), home, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Rows) != 1 {
		t.Fatalf("unexpected rows: %#v", snapshot)
	}
	row := snapshot.Rows[0]
	if row.Requests != 2 || row.InputTokens != 200 || row.CachedInputTokens != 130 || row.OutputTokens != 20 {
		t.Fatalf("last usage was not used as the response-level measurement: %#v", row)
	}
}
