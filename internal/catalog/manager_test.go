package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func TestMergedCatalogPreservesCompleteNativeDefinitionsAndUnion(t *testing.T) {
	box, _ := secure.NewBox(bytes.Repeat([]byte{3}, 32))
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	primaryDefinition := json.RawMessage(`{"slug":"gpt-native","display_name":"Native","supported_reasoning_levels":[{"effort":"ultra","description":"upstream"}],"opaque_safety":{"keep":true}}`)
	secondaryDefinition := json.RawMessage(`{"slug":"gpt-native","display_name":"Different","supported_reasoning_levels":[],"other":7}`)
	primary, _, err := store.PutAccount(context.Background(), catalogAccount("primary-stable", 20, []string{"gpt-native"}, catalogPayload(primaryDefinition)), false)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.PutAccount(context.Background(), catalogAccount("secondary-stable", 30, []string{"gpt-native", "codex-auto-review"}, catalogPayload(secondaryDefinition, json.RawMessage(`{"slug":"codex-auto-review","unknown_internal":true}`))), false)
	if err != nil {
		t.Fatal(err)
	}
	if !primary.Primary {
		t.Fatal("first account was not designated primary")
	}
	enrollment, _ := store.CreateEnrollment(context.Background(), "Catalog Mac")
	if err = store.ApproveDevice(context.Background(), enrollment.DeviceID); err != nil {
		t.Fatal(err)
	}
	result, err := NewManager(store).BuildForDevice(context.Background(), enrollment.DeviceID, "codex-cli 1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	var merged struct {
		Models []json.RawMessage `json:"models"`
	}
	if err = json.Unmarshal(result.Raw, &merged); err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]json.RawMessage)
	for _, entry := range merged.Models {
		var identity struct {
			Slug string `json:"slug"`
		}
		_ = json.Unmarshal(entry, &identity)
		entries[identity.Slug] = entry
	}
	if len(entries) != 2 {
		t.Fatalf("expected union of two models, got %v", entries)
	}
	if !jsonSemanticallyEqual(entries["gpt-native"], primaryDefinition) {
		t.Fatalf("primary native definition was changed or field-merged: %s", entries["gpt-native"])
	}
	if !bytes.Contains(entries["codex-auto-review"], []byte("unknown_internal")) {
		t.Fatal("native internal/safety model was removed or rewritten")
	}
	if _, found := result.Conflicts["gpt-native"]; !found {
		t.Fatal("native definition conflict was not reported")
	}
}

func TestExcludedThirdPartyModelsCannotBeRoutedManually(t *testing.T) {
	box, _ := secure.NewBox(bytes.Repeat([]byte{7}, 32))
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	openRouterRaw := json.RawMessage(`{"data":[{"id":"vendor/no-tools","name":"No tools","context_length":32000,"supported_parameters":[],"architecture":{"input_modalities":["text"],"output_modalities":["text"]}}]}`)
	if err = store.PutCatalogSnapshot(context.Background(), storage.CatalogSnapshot{Provider: "openrouter", Raw: openRouterRaw, FetchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ollamaDiscovery := providers.Discovery{Models: []providers.DiscoveredModel{{
		ID: "no-tools", Context: 32000, Capabilities: map[string]bool{"responses": true, "streaming": true, "completion": true}, InputModes: []string{"text"},
	}}}
	ollamaRaw, _ := json.Marshal(ollamaDiscovery)
	if err = store.PutCatalogSnapshot(context.Background(), storage.CatalogSnapshot{Provider: "ollama", Raw: ollamaRaw, FetchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store)
	if _, err = manager.OpenRouterCapabilities(context.Background(), "vendor/no-tools"); err == nil {
		t.Fatal("excluded OpenRouter model remained manually routable")
	}
	if _, err = manager.OllamaCapabilities(context.Background(), "no-tools"); err == nil {
		t.Fatal("excluded Ollama model remained manually routable")
	}
}

func TestMergedCatalogUsesDiscoveredOpenRouterCapabilities(t *testing.T) {
	box, _ := secure.NewBox(bytes.Repeat([]byte{9}, 32))
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.PutProvider(context.Background(), storage.ProviderConfig{
		Name: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Enabled: true, APIKey: "test-key",
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.PutProvider(context.Background(), storage.ProviderConfig{
		Name: "ollama", BaseURL: "http://192.168.1.4:11434", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"data":[{"id":"z-ai/glm-test","name":"GLM Test","context_length":131072,"supported_parameters":["tools","tool_choice","reasoning","reasoning_effort","verbosity","structured_outputs"],"architecture":{"input_modalities":["text","image"],"output_modalities":["text"]},"reasoning":{"mandatory":true,"default_enabled":true,"supported_efforts":["max","high","low"],"default_effort":"max"}},{"id":"vendor/plain","name":"Plain","context_length":32768,"supported_parameters":["tools","tool_choice"],"architecture":{"input_modalities":["text"],"output_modalities":["text"]}}]}`)
	if err = store.PutCatalogSnapshot(context.Background(), storage.CatalogSnapshot{
		Provider: "openrouter", Raw: raw, FetchedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	ollamaRaw, err := json.Marshal(providers.Discovery{Models: []providers.DiscoveredModel{{
		ID: "llama3.1:8b", DisplayName: "llama3.1:8b", Description: "llama", Context: 131072,
		Capabilities: map[string]bool{"responses": true, "streaming": true, "completion": true, "tools": true},
		InputModes:   []string{"text"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.PutCatalogSnapshot(context.Background(), storage.CatalogSnapshot{
		Provider: "ollama", Raw: ollamaRaw, FetchedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	enrollment, err := store.CreateEnrollment(context.Background(), "Capability Mac")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ApproveDevice(context.Background(), enrollment.DeviceID); err != nil {
		t.Fatal(err)
	}
	result, err := NewManager(store).BuildForDevice(context.Background(), enrollment.DeviceID, "codex-cli 0.150.1")
	if err != nil {
		t.Fatal(err)
	}
	var merged struct {
		Models []map[string]any `json:"models"`
	}
	if err = json.Unmarshal(result.Raw, &merged); err != nil {
		t.Fatal(err)
	}
	bySlug := make(map[string]map[string]any)
	for _, model := range merged.Models {
		bySlug[model["slug"].(string)] = model
	}
	glm := bySlug["openrouter/z-ai/glm-test"]
	if glm == nil || len(glm["supported_reasoning_levels"].([]any)) != 3 || glm["default_reasoning_level"] != "max" || glm["support_verbosity"] != true {
		t.Fatalf("GLM capabilities were discarded while building the merged catalog: %v", glm)
	}
	if glm["apply_patch_tool_type"] != "freeform" {
		t.Fatalf("verified OpenRouter tool support did not enable Codex's local patch tool: %v", glm)
	}
	levels := glm["supported_reasoning_levels"].([]any)
	if levels[0].(map[string]any)["effort"] != "low" || levels[1].(map[string]any)["effort"] != "high" || levels[2].(map[string]any)["effort"] != "max" {
		t.Fatalf("merged GLM picker modes do not match discovered metadata: %v", levels)
	}
	modalities := glm["input_modalities"].([]any)
	if len(modalities) != 2 || modalities[1] != "image" {
		t.Fatalf("GLM image capability was discarded: %v", modalities)
	}
	plain := bySlug["openrouter/vendor/plain"]
	if plain == nil || len(plain["supported_reasoning_levels"].([]any)) != 0 || plain["support_verbosity"] != false {
		t.Fatalf("unadvertised capabilities were invented for plain model: %v", plain)
	}
	llama := bySlug["ollama/llama3.1:8b"]
	if llama == nil || llama["default_reasoning_level"] != "none" {
		t.Fatalf("Ollama no-reasoning default was lost in the merged catalog: %v", llama)
	}
	if llama["apply_patch_tool_type"] != nil {
		t.Fatalf("Ollama inherited unverified OpenRouter patch metadata: %v", llama)
	}
	llamaLevels := llama["supported_reasoning_levels"].([]any)
	if len(llamaLevels) != 1 || llamaLevels[0].(map[string]any)["effort"] != "none" {
		t.Fatalf("Ollama model did not remain isolated to the no-reasoning sentinel: %v", llamaLevels)
	}
}

func catalogAccount(stable string, quota float64, models []string, snapshot []byte) storage.AccountInput {
	return storage.AccountInput{
		Credential:  storage.OpenAICredential{AccountID: stable, AccessToken: "access-" + stable, RefreshToken: "refresh-" + stable, IDToken: "id-" + stable, ExpiresAt: time.Now().Add(time.Hour)},
		MaskedEmail: "m***d@e***.com", Plan: "plus", Status: "ready", QuotaUsedPercent: quota,
		EntitledModels: models, RawCatalogSnapshot: snapshot,
	}
}

func catalogPayload(entries ...json.RawMessage) []byte {
	raw, _ := json.Marshal(struct {
		Models []json.RawMessage `json:"models"`
	}{entries})
	return raw
}

func jsonSemanticallyEqual(left, right []byte) bool {
	var l, r any
	if json.Unmarshal(left, &l) != nil || json.Unmarshal(right, &r) != nil {
		return false
	}
	lb, _ := json.Marshal(l)
	rb, _ := json.Marshal(r)
	return bytes.Equal(lb, rb)
}
