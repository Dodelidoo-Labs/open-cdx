package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers"
)

type Client struct {
	HTTP    *http.Client
	BaseURL string
}

func New(httpClient *http.Client, baseURL string, allowHTTP bool) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("Ollama URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Ollama URL must not contain credentials, query parameters, or a fragment")
	}
	if parsed.Scheme == "http" && !allowHTTP && !loopback(parsed.Hostname()) {
		return nil, errors.New("plaintext Ollama on a non-loopback address requires Allow HTTP")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/")}, nil
}

func (client *Client) DiscoverModels(ctx context.Context, _ providers.Credential, _ string) (providers.Discovery, error) {
	if err := client.requireResponsesAPI(ctx); err != nil {
		return providers.Discovery{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL+"/api/tags", nil)
	if err != nil {
		return providers.Discovery{}, err
	}
	response, err := client.HTTP.Do(request)
	if err != nil {
		return providers.Discovery{}, errors.New("Ollama model discovery failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providers.Discovery{}, fmt.Errorf("Ollama model discovery returned %s", response.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return providers.Discovery{}, err
	}
	var tags struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err = json.Unmarshal(raw, &tags); err != nil {
		return providers.Discovery{}, errors.New("Ollama tags response was invalid")
	}
	models := make([]providers.DiscoveredModel, 0, len(tags.Models))
	for _, tag := range tags.Models {
		name := tag.Model
		if name == "" {
			name = tag.Name
		}
		if name == "" {
			continue
		}
		model, showErr := client.show(ctx, name)
		if showErr != nil {
			model = providers.DiscoveredModel{ID: name, DisplayName: name, Capabilities: map[string]bool{}}
		}
		models = append(models, model)
	}
	return providers.Discovery{Models: models, Raw: raw, FetchedAt: time.Now().UTC()}, nil
}

func (client *Client) show(ctx context.Context, name string) (providers.DiscoveredModel, error) {
	payload, _ := json.Marshal(map[string]any{"model": name, "verbose": false})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.BaseURL+"/api/show", bytes.NewReader(payload))
	if err != nil {
		return providers.DiscoveredModel{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return providers.DiscoveredModel{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providers.DiscoveredModel{}, errors.New("Ollama show failed")
	}
	var detail struct {
		Capabilities []string       `json:"capabilities"`
		ModelInfo    map[string]any `json:"model_info"`
		Details      struct {
			Family string `json:"family"`
		} `json:"details"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&detail); err != nil {
		return providers.DiscoveredModel{}, err
	}
	capabilities := map[string]bool{"responses": true, "streaming": true}
	for _, capability := range detail.Capabilities {
		capabilities[capability] = true
	}
	contextLength := int64(0)
	for key, value := range detail.ModelInfo {
		if strings.HasSuffix(key, ".context_length") {
			if number, ok := value.(float64); ok && int64(number) > contextLength {
				contextLength = int64(number)
			}
		}
	}
	inputModes := []string{"text"}
	if capabilities["vision"] {
		inputModes = append(inputModes, "image")
	}
	return providers.DiscoveredModel{ID: name, DisplayName: name, Description: detail.Details.Family,
		Capabilities: capabilities, Context: contextLength, InputModes: inputModes}, nil
}

func (client *Client) TranslateCatalog(discovery providers.Discovery) ([]json.RawMessage, map[string]string, error) {
	models := append([]providers.DiscoveredModel(nil), discovery.Models...)
	sort.Slice(models, func(left, right int) bool { return models[left].ID < models[right].ID })
	entries := make([]json.RawMessage, 0, len(models))
	excluded := make(map[string]string)
	for index, model := range models {
		reason := CompatibilityReason(model)
		if reason != "" {
			excluded[model.ID] = reason
			continue
		}
		entry, err := providers.BuildCodexModel(providers.CodexModelOptions{
			Slug: "ollama/" + model.ID, DisplayName: "Ollama · " + model.DisplayName,
			Description: model.Description, Context: model.Context, Priority: 2000 + index,
			ImageInput: model.Capabilities["vision"],
			// `none` is a Codex-facing no-op sentinel, not an advertised Ollama
			// reasoning capability. A single level lets Codex complete model
			// selection without leaving its picker open. ValidateRequest removes
			// the resulting no-op object before the request reaches Ollama.
			ReasoningLevels:       []providers.ReasoningLevel{{Effort: "none", Description: "No reasoning"}},
			DefaultReasoningLevel: "none",
		})
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, entry)
	}
	return entries, excluded, nil
}

func (client *Client) ResponsesURL(path string) (string, error) {
	if path != "/v1/responses" {
		return "", errors.New("Ollama supports only non-stateful Responses requests")
	}
	return client.BaseURL + "/v1/responses", nil
}

func (client *Client) PrepareRequest(request *http.Request, _ providers.Credential, _ string) error {
	stripCodexOnlyHeaders(request.Header)
	request.Header.Set("Authorization", "Bearer ollama")
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

func CompatibilityReason(model providers.DiscoveredModel) string {
	switch {
	case !model.Capabilities["responses"] || !model.Capabilities["streaming"]:
		return "non-stateful Responses streaming support is not established"
	case !model.Capabilities["completion"]:
		return "completion capability is not advertised"
	case !model.Capabilities["tools"]:
		return "tool/function calling capability is not advertised"
	case model.Context <= 0:
		return "context length is not published"
	default:
		return ""
	}
}

func (client *Client) requireResponsesAPI(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL+"/api/version", nil)
	if err != nil {
		return err
	}
	response, err := client.HTTP.Do(request)
	if err != nil {
		return errors.New("Ollama version discovery failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Ollama version discovery returned %s", response.Status)
	}
	var payload struct {
		Version string `json:"version"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil || !versionAtLeast(payload.Version, 0, 13, 3) {
		return errors.New("Ollama 0.13.3 or newer is required for the non-stateful Responses API")
	}
	return nil
}

func versionAtLeast(value string, minimumMajor, minimumMinor, minimumPatch int) bool {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	core := strings.SplitN(value, "-", 2)[0]
	parts := strings.Split(core, ".")
	if len(parts) < 3 {
		return false
	}
	parsed := [3]int{}
	for index := range parsed {
		number, err := strconv.Atoi(parts[index])
		if err != nil || number < 0 {
			return false
		}
		parsed[index] = number
	}
	minimum := [3]int{minimumMajor, minimumMinor, minimumPatch}
	for index := range parsed {
		if parsed[index] != minimum[index] {
			return parsed[index] > minimum[index]
		}
	}
	// A prerelease of the minimum stable version is conservatively too old.
	return !strings.Contains(value, "-")
}

func ValidateRequest(body map[string]any) error {
	if _, present := body["previous_response_id"]; present {
		return errors.New("Ollama does not support stateful previous_response_id requests")
	}
	if _, present := body["conversation"]; present {
		return errors.New("Ollama does not support stateful conversation requests")
	}
	if value, present := body["reasoning"]; present {
		reasoning, ok := value.(map[string]any)
		if !ok {
			return errors.New("Ollama reasoning controls must be a JSON object")
		}
		if noOpReasoning(reasoning) {
			delete(body, "reasoning")
		} else {
			return errors.New("Ollama reasoning controls are not advertised per model in this catalog")
		}
	}
	if value, present := body["text"]; present && meaningful(value) {
		return errors.New("Ollama Responses does not document non-empty Codex text controls")
	}
	removeHostedWebSearch(body)
	return nil
}

// Codex 0.150.1 exposes hosted web search for every configured Responses
// provider; supports_search_tool is unrelated and controls deferred tool
// discovery. Ollama does not execute OpenAI's hosted search tool, so keep the
// client-executed tools and remove only that provider-hosted entry.
func removeHostedWebSearch(body map[string]any) {
	rawTools, present := body["tools"]
	if !present {
		return
	}
	tools, ok := rawTools.([]any)
	if !ok {
		return
	}
	filtered := make([]any, 0, len(tools))
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if ok {
			kind, _ := tool["type"].(string)
			if kind == "web_search" || kind == "web_search_preview" {
				continue
			}
		}
		filtered = append(filtered, rawTool)
	}
	if len(filtered) == 0 {
		delete(body, "tools")
		if choice, _ := body["tool_choice"].(string); choice == "auto" || choice == "none" {
			delete(body, "tool_choice")
		}
		return
	}
	body["tools"] = filtered
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

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
