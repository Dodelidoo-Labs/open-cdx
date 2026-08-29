package ollama

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/opencdx/opencdx/internal/providers"
)

func TestCatalogUsesOnlyNoReasoningSentinel(t *testing.T) {
	client, err := New(nil, "http://127.0.0.1:11434", false)
	if err != nil {
		t.Fatal(err)
	}
	discovery := providers.Discovery{Models: []providers.DiscoveredModel{{
		ID: "llama3.1:8b", DisplayName: "llama3.1:8b", Description: "llama", Context: 131072,
		Capabilities: map[string]bool{"responses": true, "streaming": true, "completion": true, "tools": true},
		InputModes:   []string{"text"},
	}}}
	entries, excluded, err := client.TranslateCatalog(discovery)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || len(excluded) != 0 {
		t.Fatalf("unexpected Ollama catalog result: entries=%d exclusions=%v", len(entries), excluded)
	}
	var entry struct {
		DefaultReasoningLevel    string `json:"default_reasoning_level"`
		ApplyPatchToolType       any    `json:"apply_patch_tool_type"`
		SupportedReasoningLevels []struct {
			Effort string `json:"effort"`
		} `json:"supported_reasoning_levels"`
	}
	if err = json.Unmarshal(entries[0], &entry); err != nil {
		t.Fatal(err)
	}
	if entry.DefaultReasoningLevel != "none" || len(entry.SupportedReasoningLevels) != 1 || entry.SupportedReasoningLevels[0].Effort != "none" {
		t.Fatalf("Ollama model did not expose exactly the no-reasoning sentinel: %s", entries[0])
	}
	if entry.ApplyPatchToolType != nil {
		t.Fatalf("unverified Ollama custom-tool support was advertised: %s", entries[0])
	}
}

func TestResponsesVersionFloor(t *testing.T) {
	for _, value := range []string{"0.13.3", "v0.13.4", "0.14.0", "1.0.0"} {
		if !versionAtLeast(value, 0, 13, 3) {
			t.Fatalf("supported Ollama version %q was rejected", value)
		}
	}
	for _, value := range []string{"", "0.13.2", "0.13.3-rc1", "0.12.9", "not-a-version"} {
		if versionAtLeast(value, 0, 13, 3) {
			t.Fatalf("unsupported Ollama version %q was accepted", value)
		}
	}
}

func TestPrepareRequestRemovesOpenAIOnlyMetadata(t *testing.T) {
	client, err := New(nil, "http://127.0.0.1:11434", false)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:11434/v1/responses", nil)
	request.Header.Set("X-Oai-Attestation", "private-openai-attestation")
	request.Header.Set("Session-Id", "native-session")
	if err = client.PrepareRequest(request, providers.Credential{}, "model"); err != nil {
		t.Fatal(err)
	}
	if request.Header.Get("X-Oai-Attestation") != "" || request.Header.Get("Session-Id") != "" || request.Header.Get("Authorization") != "Bearer ollama" {
		t.Fatal("Ollama header isolation failed")
	}
}

func TestNoOpReasoningIsRemoved(t *testing.T) {
	body := map[string]any{"reasoning": map[string]any{"effort": "none"}}
	if err := ValidateRequest(body); err != nil {
		t.Fatalf("Codex no-op reasoning was rejected: %v", err)
	}
	if _, present := body["reasoning"]; present {
		t.Fatal("Codex no-op reasoning was forwarded to Ollama")
	}
	if err := ValidateRequest(map[string]any{"reasoning": map[string]any{"effort": "high"}}); err == nil {
		t.Fatal("unadvertised Ollama reasoning effort was accepted")
	}
}

func TestHostedWebSearchIsRemovedWithoutLosingClientTools(t *testing.T) {
	body := map[string]any{
		"tool_choice": "auto",
		"tools": []any{
			map[string]any{"type": "web_search", "external_web_access": true},
			map[string]any{"type": "function", "name": "exec_command"},
		},
	}
	if err := ValidateRequest(body); err != nil {
		t.Fatal(err)
	}
	tools := body["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "exec_command" {
		t.Fatalf("Ollama tool normalization kept hosted search or lost client tools: %#v", tools)
	}
	if body["tool_choice"] != "auto" {
		t.Fatalf("tool choice was removed while a client tool remained: %#v", body)
	}

	onlySearch := map[string]any{
		"tool_choice": "auto",
		"tools":       []any{map[string]any{"type": "web_search_preview"}},
	}
	if err := ValidateRequest(onlySearch); err != nil {
		t.Fatal(err)
	}
	if _, present := onlySearch["tools"]; present {
		t.Fatalf("sole hosted search tool was not removed: %#v", onlySearch)
	}
	if _, present := onlySearch["tool_choice"]; present {
		t.Fatalf("orphaned automatic tool choice was not removed: %#v", onlySearch)
	}
}
