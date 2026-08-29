package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/opencdx/opencdx/internal/providers"
)

type Client struct {
	HTTP    *http.Client
	BaseURL string
	APIKey  string
}

func New(httpClient *http.Client, baseURL, apiKey string) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("OpenRouter base URL must be an absolute HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("OpenRouter base URL must not contain credentials, query parameters, or a fragment")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey}, nil
}

func (client *Client) DiscoverModels(ctx context.Context, _ providers.Credential, _ string) (providers.Discovery, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL+"/models", nil)
	if err != nil {
		return providers.Discovery{}, err
	}
	client.addAuth(request.Header)
	response, err := client.HTTP.Do(request)
	if err != nil {
		return providers.Discovery{}, errors.New("OpenRouter model discovery failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providers.Discovery{}, fmt.Errorf("OpenRouter model discovery returned %s", response.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return providers.Discovery{}, err
	}
	discovery, err := ParseDiscovery(raw)
	if err != nil {
		return providers.Discovery{}, err
	}
	discovery.ETag = response.Header.Get("ETag")
	discovery.FetchedAt = time.Now().UTC()
	return discovery, nil
}

func (client *Client) TranslateCatalog(discovery providers.Discovery) ([]json.RawMessage, map[string]string, error) {
	models := append([]providers.DiscoveredModel(nil), discovery.Models...)
	sort.Slice(models, func(left, right int) bool { return models[left].ID < models[right].ID })
	entries := make([]json.RawMessage, 0, len(models))
	excluded := make(map[string]string)
	priority := 1000
	for _, model := range models {
		reason := CompatibilityReason(model)
		if reason != "" {
			excluded[model.ID] = reason
			continue
		}
		image := false
		for _, mode := range model.InputModes {
			image = image || mode == "image"
		}
		reasoningLevels := reasoningLevels(model)
		defaultReasoningLevel := ""
		if len(reasoningLevels) > 0 && contains(model.ReasoningEfforts, model.DefaultReasoningEffort) {
			defaultReasoningLevel = model.DefaultReasoningEffort
		}
		entry, err := providers.BuildCodexModel(providers.CodexModelOptions{
			Slug: "openrouter/" + model.ID, DisplayName: "OpenRouter · " + model.DisplayName,
			Description: model.Description, Context: model.Context, Priority: priority, ImageInput: image,
			// Every admitted OpenRouter model publishes both tools and tool_choice.
			// OpenRouter's Responses endpoint accepts Codex's freeform custom tool,
			// while Codex itself applies the resulting patch on the local machine.
			EnableApplyPatch: true,
			ReasoningLevels:  reasoningLevels, DefaultReasoningLevel: defaultReasoningLevel,
			SupportsVerbosity: model.Capabilities["verbosity"],
		})
		if err != nil {
			return nil, nil, err
		}
		priority++
		entries = append(entries, entry)
	}
	return entries, excluded, nil
}

func (client *Client) ResponsesURL(path string) (string, error) {
	if path != "/v1/responses" {
		return "", errors.New("OpenRouter supports only the Responses endpoint for routed models")
	}
	return client.BaseURL + "/responses", nil
}

func (client *Client) PrepareRequest(request *http.Request, _ providers.Credential, _ string) error {
	stripCodexOnlyHeaders(request.Header)
	client.addAuth(request.Header)
	return nil
}

func stripCodexOnlyHeaders(headers http.Header) {
	for name := range headers {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-codex-") || strings.HasPrefix(lower, "x-openai-") ||
			strings.HasPrefix(lower, "x-oai-") || strings.HasPrefix(lower, "x-responsesapi-") {
			headers.Del(name)
			continue
		}
		switch lower {
		case "openai-beta", "originator", "version", "session-id", "thread-id":
			headers.Del(name)
		}
	}
}

func (client *Client) Health(ctx context.Context) error {
	_, err := client.DiscoverModels(ctx, providers.Credential{}, "")
	return err
}

func (client *Client) addAuth(headers http.Header) {
	if client.APIKey != "" {
		headers.Set("Authorization", "Bearer "+client.APIKey)
	}
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", "opencdx-router/0.1")
	}
}

type modelPayload struct {
	Data []struct {
		ID                  string   `json:"id"`
		Name                string   `json:"name"`
		Description         string   `json:"description"`
		ContextLength       int64    `json:"context_length"`
		SupportedParameters []string `json:"supported_parameters"`
		Architecture        struct {
			InputModalities  []string `json:"input_modalities"`
			OutputModalities []string `json:"output_modalities"`
		} `json:"architecture"`
		Reasoning *struct {
			Mandatory      bool     `json:"mandatory"`
			DefaultEnabled bool     `json:"default_enabled"`
			Supported      []string `json:"supported_efforts"`
			Default        string   `json:"default_effort"`
		} `json:"reasoning"`
	} `json:"data"`
}

func ParseDiscovery(raw []byte) (providers.Discovery, error) {
	var payload modelPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return providers.Discovery{}, errors.New("OpenRouter model catalog was invalid")
	}
	models := make([]providers.DiscoveredModel, 0, len(payload.Data))
	for _, model := range payload.Data {
		if model.ID == "" {
			continue
		}
		capabilities := map[string]bool{"responses": true, "streaming": true}
		for _, parameter := range model.SupportedParameters {
			capabilities[parameter] = true
		}
		for _, output := range model.Architecture.OutputModalities {
			capabilities["output:"+output] = true
		}
		var reasoningEfforts []string
		var defaultReasoningEffort string
		var reasoningMandatory, reasoningDefaultEnabled bool
		if model.Reasoning != nil {
			reasoningEfforts = uniqueNonEmpty(model.Reasoning.Supported)
			defaultReasoningEffort = strings.TrimSpace(model.Reasoning.Default)
			reasoningMandatory = model.Reasoning.Mandatory
			reasoningDefaultEnabled = model.Reasoning.DefaultEnabled
		}
		models = append(models, providers.DiscoveredModel{
			ID: model.ID, DisplayName: model.Name, Description: model.Description,
			Context: model.ContextLength, Capabilities: capabilities, InputModes: model.Architecture.InputModalities,
			ReasoningEfforts: reasoningEfforts, DefaultReasoningEffort: defaultReasoningEffort,
			ReasoningMandatory: reasoningMandatory, ReasoningDefaultEnabled: reasoningDefaultEnabled,
		})
	}
	return providers.Discovery{Models: models, Raw: append(json.RawMessage(nil), raw...)}, nil
}

func CompatibilityReason(model providers.DiscoveredModel) string {
	if !model.Capabilities["responses"] || !model.Capabilities["streaming"] {
		return "Responses streaming support is not established"
	}
	if model.Context <= 0 {
		return "context length is not published"
	}
	if !contains(model.InputModes, "text") || !model.Capabilities["output:text"] {
		return "text input and output are required"
	}
	if !model.Capabilities["tools"] {
		return "tool/function calling is not advertised"
	}
	if !model.Capabilities["tool_choice"] {
		return "tool choice control is not advertised"
	}
	return ""
}

func ValidateRequest(body map[string]any, model providers.DiscoveredModel) error {
	capabilities := model.Capabilities
	if err := normalizeReasoning(body, model); err != nil {
		return err
	}
	if text, ok := body["text"].(map[string]any); ok {
		if value, present := text["verbosity"]; present && meaningful(value) && !capabilities["verbosity"] {
			return errors.New("destination model does not advertise verbosity control")
		}
		if value, present := text["format"]; present && meaningful(value) && !capabilities["structured_outputs"] && !capabilities["response_format"] {
			return errors.New("destination model does not advertise structured output")
		}
	}
	for _, parameter := range []string{"temperature", "top_p", "frequency_penalty", "presence_penalty"} {
		if value, present := body[parameter]; present && meaningful(value) && !capabilities[parameter] {
			return fmt.Errorf("destination model does not advertise %s", parameter)
		}
	}
	if tier, ok := body["service_tier"].(string); ok && tier != "" && tier != "auto" && tier != "default" {
		return errors.New("OpenAI service tiers are not mapped to OpenRouter")
	}
	return nil
}

func reasoningLevels(model providers.DiscoveredModel) []providers.ReasoningLevel {
	if !model.Capabilities["reasoning"] || !model.Capabilities["reasoning_effort"] || len(model.ReasoningEfforts) == 0 {
		return nil
	}
	efforts := append([]string(nil), model.ReasoningEfforts...)
	sort.SliceStable(efforts, func(left, right int) bool {
		leftRank, rightRank := reasoningEffortRank(efforts[left]), reasoningEffortRank(efforts[right])
		if leftRank == rightRank {
			return efforts[left] < efforts[right]
		}
		return leftRank < rightRank
	})
	levels := make([]providers.ReasoningLevel, 0, len(efforts))
	for _, effort := range efforts {
		levels = append(levels, providers.ReasoningLevel{Effort: effort, Description: reasoningEffortDescription(effort)})
	}
	return levels
}

func normalizeReasoning(body map[string]any, model providers.DiscoveredModel) error {
	value, present := body["reasoning"]
	if !present || value == nil {
		return nil
	}
	reasoning, ok := value.(map[string]any)
	if !ok {
		return errors.New("reasoning controls must be a JSON object")
	}
	if len(reasoning) == 0 {
		delete(body, "reasoning")
		return nil
	}

	// Codex can emit an explicit no-op effort inherited from the user's global
	// configuration even when a catalog entry advertises no reasoning levels.
	// Removing that no-op lets non-reasoning destinations behave as advertised.
	if noOpReasoning(reasoning) && len(reasoningLevels(model)) == 0 {
		delete(body, "reasoning")
		return nil
	}
	if len(reasoningLevels(model)) == 0 {
		return errors.New("destination model does not publish supported reasoning efforts")
	}
	for name, raw := range reasoning {
		switch name {
		case "effort":
			effort, ok := raw.(string)
			if !ok || !contains(model.ReasoningEfforts, effort) {
				return fmt.Errorf("OpenRouter reasoning effort %q is not advertised for the destination model (supported: %s)", effort, strings.Join(model.ReasoningEfforts, ", "))
			}
		case "summary":
			if raw != nil && raw != "" && raw != "none" {
				return errors.New("destination model does not advertise reasoning summary control")
			}
			delete(reasoning, name)
		default:
			if meaningful(raw) {
				return fmt.Errorf("destination model does not advertise reasoning.%s control", name)
			}
			delete(reasoning, name)
		}
	}
	if len(reasoning) == 0 {
		delete(body, "reasoning")
	}
	return nil
}

func noOpReasoning(reasoning map[string]any) bool {
	for name, value := range reasoning {
		switch name {
		case "effort", "summary":
			if value != nil && value != "" && value != "none" {
				return false
			}
		default:
			if meaningful(value) {
				return false
			}
		}
	}
	return true
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func reasoningEffortRank(effort string) int {
	for index, value := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"} {
		if effort == value {
			return index
		}
	}
	return 100
}

func reasoningEffortDescription(effort string) string {
	switch effort {
	case "none":
		return "No reasoning"
	case "minimal":
		return "Minimal reasoning"
	case "low":
		return "Light reasoning"
	case "medium":
		return "Balanced reasoning"
	case "high":
		return "Deep reasoning"
	case "xhigh":
		return "Extra-high reasoning"
	case "max":
		return "Maximum reasoning"
	case "ultra":
		return "Maximum reasoning with delegation"
	default:
		return "Provider-advertised reasoning effort"
	}
}

func meaningful(value any) bool {
	if value == nil {
		return false
	}
	if object, ok := value.(map[string]any); ok {
		return len(object) > 0
	}
	if text, ok := value.(string); ok {
		return text != ""
	}
	return true
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
