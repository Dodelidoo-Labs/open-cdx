package openrouter

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers"
)

func TestCapabilityMappingAndReasoningControls(t *testing.T) {
	client, err := New(nil, "https://openrouter.ai/api/v1", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	discovery := providers.Discovery{Models: []providers.DiscoveredModel{
		{ID: "vendor/compatible", DisplayName: "Compatible", Context: 64000, InputModes: []string{"text", "image"}, Capabilities: map[string]bool{"responses": true, "streaming": true, "output:text": true, "tools": true, "tool_choice": true, "reasoning": true, "reasoning_effort": true, "verbosity": true}, ReasoningEfforts: []string{"max", "high", "low"}, DefaultReasoningEffort: "max", ReasoningMandatory: true, ReasoningDefaultEnabled: true},
		{ID: "vendor/unknown-tools", DisplayName: "Unknown", Context: 64000, InputModes: []string{"text"}, Capabilities: map[string]bool{"responses": true, "streaming": true, "output:text": true}},
	}}
	entries, excluded, err := client.TranslateCatalog(discovery)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.Contains(excluded["vendor/unknown-tools"], "tool") {
		t.Fatalf("unexpected mapping entries=%d exclusions=%v", len(entries), excluded)
	}
	var entry map[string]any
	if err = json.Unmarshal(entries[0], &entry); err != nil {
		t.Fatal(err)
	}
	levels := entry["supported_reasoning_levels"].([]any)
	if entry["slug"] != "openrouter/vendor/compatible" || entry["supports_reasoning_summary_parameter"] != false || len(levels) != 3 || entry["support_verbosity"] != true || entry["default_reasoning_level"] != "max" {
		t.Fatalf("translated model did not advertise verified reasoning support: %s", entries[0])
	}
	if entry["apply_patch_tool_type"] != "freeform" {
		t.Fatalf("tool-capable OpenRouter model did not receive Codex's local patch tool: %s", entries[0])
	}
	wantEfforts := []string{"low", "high", "max"}
	for index, level := range levels {
		if level.(map[string]any)["effort"] != wantEfforts[index] {
			t.Fatalf("picker efforts do not match the model metadata: %v", levels)
		}
	}
	if entry["base_instructions"] != "" || entry["model_messages"].(map[string]any)["instructions_template"] != "" {
		t.Fatalf("translated model contained router-authored prompt text: %s", entries[0])
	}
	if err = ValidateRequest(map[string]any{"reasoning": map[string]any{"effort": "high"}}, discovery.Models[0]); err != nil {
		t.Fatalf("advertised reasoning effort was rejected: %v", err)
	}
	if err = ValidateRequest(map[string]any{"reasoning": map[string]any{"effort": "max"}}, discovery.Models[0]); err != nil {
		t.Fatalf("model-advertised max reasoning was rejected: %v", err)
	}
	if err = ValidateRequest(map[string]any{"reasoning": map[string]any{"effort": "medium"}}, discovery.Models[0]); err == nil {
		t.Fatal("a reasoning effort absent from the model metadata was accepted")
	}
	genericReasoning := providers.DiscoveredModel{Capabilities: map[string]bool{"reasoning": true, "reasoning_effort": true}}
	if err = ValidateRequest(map[string]any{"reasoning": map[string]any{"effort": "high"}}, genericReasoning); err == nil {
		t.Fatal("parameter names without exact supported efforts were treated as proof")
	}
	if err = ValidateRequest(map[string]any{"reasoning": nil, "text": map[string]any{}}, discovery.Models[0]); err != nil {
		t.Fatalf("empty optional controls should be accepted: %v", err)
	}
	noReasoning := map[string]any{"reasoning": map[string]any{"effort": "none"}}
	if err = ValidateRequest(noReasoning, providers.DiscoveredModel{Capabilities: map[string]bool{}}); err != nil {
		t.Fatalf("no-op reasoning should be accepted: %v", err)
	}
	if _, present := noReasoning["reasoning"]; present {
		t.Fatal("no-op reasoning was not removed for a non-reasoning model")
	}
}

func TestParseDiscoveryPreservesModelSpecificReasoningMetadata(t *testing.T) {
	raw := []byte(`{"data":[{"id":"z-ai/glm-5.3-flash","name":"GLM","context_length":1310720,"supported_parameters":["tools","tool_choice","reasoning","reasoning_effort"],"architecture":{"input_modalities":["text"],"output_modalities":["text"]},"reasoning":{"mandatory":true,"default_enabled":true,"supported_efforts":["max","high","low"],"default_effort":"max"}},{"id":"vendor/high-only","name":"High only","context_length":32000,"supported_parameters":["tools","tool_choice","reasoning","reasoning_effort"],"architecture":{"input_modalities":["text"],"output_modalities":["text"]},"reasoning":{"mandatory":false,"default_enabled":true,"supported_efforts":["high"],"default_effort":"high"}}]}`)
	discovery, err := ParseDiscovery(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Models) != 2 || strings.Join(discovery.Models[0].ReasoningEfforts, ",") != "max,high,low" || discovery.Models[0].DefaultReasoningEffort != "max" || !discovery.Models[0].ReasoningMandatory {
		t.Fatalf("GLM reasoning metadata was not preserved: %+v", discovery.Models[0])
	}
	if strings.Join(discovery.Models[1].ReasoningEfforts, ",") != "high" || discovery.Models[1].DefaultReasoningEffort != "high" {
		t.Fatalf("model-specific effort set was replaced by a global list: %+v", discovery.Models[1])
	}
}

func TestPrepareRequestRemovesOpenAIOnlyMetadata(t *testing.T) {
	client, err := New(nil, "https://openrouter.ai/api/v1", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, "https://openrouter.ai/api/v1/responses", nil)
	request.Header.Set("X-Oai-Attestation", "private-openai-attestation")
	request.Header.Set("X-Codex-Test", "native-feature")
	request.Header.Set("Thread-Id", "native-thread")
	request.Header.Set("X-OpenRouter-Title", "keep-provider-header")
	if err = client.PrepareRequest(request, providers.Credential{}, "vendor/model"); err != nil {
		t.Fatal(err)
	}
	if request.Header.Get("X-Oai-Attestation") != "" || request.Header.Get("X-Codex-Test") != "" || request.Header.Get("Thread-Id") != "" {
		t.Fatal("OpenAI-only metadata was forwarded to OpenRouter")
	}
	if request.Header.Get("X-OpenRouter-Title") != "keep-provider-header" || request.Header.Get("Authorization") != "Bearer test-key" {
		t.Fatal("OpenRouter metadata or authentication was lost")
	}
}
