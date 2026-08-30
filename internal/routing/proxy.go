package routing

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/accounts"
	"github.com/Dodelidoo-Labs/open-cdx/internal/catalog"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/ollama"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openrouter"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

const maxRequestBody = 64 << 20

type Proxy struct {
	store       *storage.Store
	accounts    *accounts.Manager
	catalog     *catalog.Manager
	selector    *Selector
	status      *StatusRegistry
	httpClient  *http.Client
	insecureDev bool
}

type DeviceContext struct {
	ID string
}

type routeTarget struct {
	provider      string
	model         string
	upstreamModel string
	account       storage.Account
	executor      providers.ResponsesExecutor
	credential    providers.Credential
	url           string
}

func NewProxy(store *storage.Store, accountManager *accounts.Manager, catalogManager *catalog.Manager, selector *Selector, status *StatusRegistry, httpClient *http.Client, insecureDev bool) *Proxy {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 0}
	}
	return &Proxy{store: store, accounts: accountManager, catalog: catalogManager, selector: selector, status: status, httpClient: httpClient, insecureDev: insecureDev}
}

func (proxy *Proxy) ServeDeviceHTTP(writer http.ResponseWriter, request *http.Request, device DeviceContext) {
	if request.Method != http.MethodPost {
		writeProxyError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported")
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxRequestBody+1))
	if err != nil || len(body) > maxRequestBody {
		writeProxyError(writer, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the router limit")
		return
	}
	var rawDocument map[string]json.RawMessage
	if err = json.Unmarshal(body, &rawDocument); err != nil {
		writeProxyError(writer, http.StatusBadRequest, "invalid_json", "request body must be a JSON object")
		return
	}
	var modelID string
	_ = json.Unmarshal(rawDocument["model"], &modelID)
	if modelID == "" {
		writeProxyError(writer, http.StatusBadRequest, "missing_model", "request body must include a model")
		return
	}
	providerName, upstreamModel := catalog.RouteIdentity(modelID)
	forwardBody := body
	if providerName != "openai" {
		rawDocument["model"], _ = json.Marshal(upstreamModel)
		forwardBody, err = json.Marshal(rawDocument)
		if err != nil {
			writeProxyError(writer, http.StatusBadRequest, "invalid_request", "request could not be translated")
			return
		}
	}
	var document map[string]any
	if err = json.Unmarshal(forwardBody, &document); err != nil {
		writeProxyError(writer, http.StatusBadRequest, "invalid_request", "request could not be validated")
		return
	}
	affinity := request.Header.Get("thread-id")
	if affinity == "" {
		affinity = request.Header.Get("session-id")
	}
	target, err := proxy.resolveTarget(request.Context(), providerName, modelID, upstreamModel, request.URL.Path, device.ID, affinity, "")
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		proxy.reportError(device.ID, err)
		writeProxyError(writer, http.StatusServiceUnavailable, "route_unavailable", publicRouteError(err))
		return
	}
	if err = proxy.validateThirdParty(request.Context(), target, document); err != nil {
		if request.Context().Err() != nil {
			return
		}
		writeProxyError(writer, http.StatusBadRequest, "unsupported_parameter", err.Error())
		return
	}
	if providerName != "openai" {
		forwardBody, err = json.Marshal(document)
		if err != nil {
			writeProxyError(writer, http.StatusBadRequest, "invalid_request", "request could not be normalized")
			return
		}
	}
	response, err := proxy.attempt(request.Context(), request, target, forwardBody)
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		proxy.reportError(device.ID, err)
		writeProxyError(writer, http.StatusBadGateway, "upstream_unavailable", "the selected provider could not be reached")
		return
	}
	// Authentication can be refreshed safely here because no response bytes have
	// reached Codex. Never retry once streaming begins.
	if target.provider == "openai" && response.StatusCode == http.StatusUnauthorized {
		response.Body.Close()
		credential, refreshErr := proxy.accounts.ForceRefreshCredential(request.Context(), target.account.ID, target.credential.AccessToken)
		if refreshErr == nil {
			target.credential = credential
			response, err = proxy.attempt(request.Context(), request, target, forwardBody)
		}
		if refreshErr != nil || err != nil {
			if request.Context().Err() != nil {
				return
			}
			proxy.reportError(device.ID, errors.New("OpenAI account requires reauthentication"))
			writeProxyError(writer, http.StatusBadGateway, "reauthentication_required", "the selected OpenAI account requires reauthentication")
			return
		}
		if response.StatusCode == http.StatusUnauthorized {
			response.Body.Close()
			_ = proxy.store.SetAccountStatus(request.Context(), target.account.ID, "reauthentication_required", "OpenAI rejected refreshed authentication")
			writeProxyError(writer, http.StatusBadGateway, "reauthentication_required", "the selected OpenAI account requires reauthentication")
			return
		}
	}
	if target.provider == "openai" && response.StatusCode == http.StatusTooManyRequests {
		response.Body.Close()
		_ = proxy.store.MarkAccountExhausted(request.Context(), target.account.ID, quotaReset(response.Header))
		nextTarget, selectErr := proxy.resolveTarget(request.Context(), "openai", modelID, upstreamModel, request.URL.Path, device.ID, affinity, target.account.ID)
		if selectErr == nil {
			target = nextTarget
			response, err = proxy.attempt(request.Context(), request, target, forwardBody)
		}
		if selectErr != nil || err != nil {
			if request.Context().Err() != nil {
				return
			}
			writeProxyError(writer, http.StatusTooManyRequests, "quota_exhausted", "all eligible OpenAI accounts are currently exhausted")
			return
		}
		if response.StatusCode == http.StatusTooManyRequests {
			response.Body.Close()
			_ = proxy.store.MarkAccountExhausted(request.Context(), target.account.ID, quotaReset(response.Header))
			writeProxyError(writer, http.StatusTooManyRequests, "quota_exhausted", "all eligible OpenAI accounts are currently exhausted")
			return
		}
	}
	defer response.Body.Close()
	copyResponseHeaders(writer.Header(), response.Header)
	writer.Header().Set("X-OpenCDX-Provider", target.provider)
	writer.Header().Set("X-OpenCDX-Model", modelID)
	if target.provider == "openai" {
		writer.Header().Set("X-OpenCDX-Account", target.account.MaskedEmail)
		writer.Header().Set("X-OpenCDX-Quota-Remaining", strconv.FormatFloat(max(0, 100-target.account.QuotaUsedPercent), 'f', 1, 64))
		if !target.account.QuotaResetAt.IsZero() {
			writer.Header().Set("X-OpenCDX-Quota-Reset", target.account.QuotaResetAt.Format(time.RFC3339))
		}
	}
	writer.WriteHeader(response.StatusCode)
	collector := newTailCollector(256 << 10)
	_, copyErr := copyStreaming(writer, io.TeeReader(response.Body, collector))
	inputTokens, outputTokens := collector.usage()
	_ = proxy.store.RecordUsage(context.WithoutCancel(request.Context()), target.provider, modelID, target.account.ID, inputTokens, outputTokens)
	streamHealthy := streamEndedNormally(request.Context(), collector, copyErr)
	proxy.status.Update(device.ID, func(status *RouteStatus) {
		status.Connected = streamHealthy
		status.State = map[bool]string{true: "connected", false: "degraded"}[streamHealthy]
		status.Provider = target.provider
		status.Model = modelID
		status.Account = target.account.MaskedEmail
		status.QuotaRemaining = max(0, 100-target.account.QuotaUsedPercent)
		status.QuotaResetAt = target.account.QuotaResetAt
		status.LastRequestAt = time.Now().UTC()
		if !streamHealthy {
			status.LastError = "upstream response stream disconnected"
		} else {
			status.LastError = ""
		}
	})
}

func (proxy *Proxy) resolveTarget(ctx context.Context, providerName, modelID, upstreamModel, path, deviceID, affinity, excludedAccount string) (routeTarget, error) {
	target := routeTarget{provider: providerName, model: modelID, upstreamModel: upstreamModel}
	switch providerName {
	case "openai":
		var selection Selection
		var err error
		if excludedAccount == "" {
			selection, err = proxy.selector.SelectNative(ctx, deviceID, modelID, affinity, "")
		} else {
			selection, err = proxy.selector.Rebind(ctx, deviceID, modelID, affinity, excludedAccount)
		}
		if err != nil {
			return routeTarget{}, err
		}
		credential, err := proxy.accounts.FreshCredential(ctx, selection.Account.ID)
		if err != nil {
			return routeTarget{}, err
		}
		target.account, target.credential, target.executor = selection.Account, credential, proxy.accounts.Client()
	case "openrouter":
		provider, err := proxy.store.Provider(ctx, "openrouter", true)
		if err != nil || !provider.Enabled || provider.APIKey == "" {
			return routeTarget{}, errors.New("OpenRouter is not configured")
		}
		client, err := openrouter.New(proxy.httpClient, provider.BaseURL, provider.APIKey)
		if err != nil {
			return routeTarget{}, err
		}
		target.executor = client
	case "ollama":
		provider, err := proxy.store.Provider(ctx, "ollama", false)
		if err != nil || !provider.Enabled {
			return routeTarget{}, errors.New("Ollama is not configured")
		}
		client, err := ollama.New(proxy.httpClient, provider.BaseURL, proxy.insecureDev)
		if err != nil {
			return routeTarget{}, err
		}
		target.executor = client
	default:
		return routeTarget{}, errors.New("unknown route namespace")
	}
	url, err := target.executor.ResponsesURL(path)
	if err != nil {
		return routeTarget{}, err
	}
	target.url = url
	return target, nil
}

func (proxy *Proxy) validateThirdParty(ctx context.Context, target routeTarget, body map[string]any) error {
	switch target.provider {
	case "openrouter":
		model, err := proxy.catalog.OpenRouterModel(ctx, target.upstreamModel)
		if err != nil {
			return errors.New("OpenRouter model capabilities are unavailable; refresh the catalog")
		}
		return openrouter.ValidateRequest(body, model)
	case "ollama":
		if _, err := proxy.catalog.OllamaCapabilities(ctx, target.upstreamModel); err != nil {
			return err
		}
		return ollama.ValidateRequest(body)
	default:
		return nil
	}
}

func (proxy *Proxy) attempt(ctx context.Context, source *http.Request, target routeTarget, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyRequestHeaders(request.Header, source.Header)
	request.Header.Set("Content-Length", strconv.Itoa(len(body)))
	request.ContentLength = int64(len(body))
	if err = target.executor.PrepareRequest(request, target.credential, target.upstreamModel); err != nil {
		return nil, err
	}
	return proxy.httpClient.Do(request)
}

func (proxy *Proxy) reportError(deviceID string, err error) {
	proxy.status.Update(deviceID, func(status *RouteStatus) {
		status.Connected = false
		status.State = "error"
		status.LastError = publicRouteError(err)
	})
}

var requestBlockedHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "cookie": true, "chatgpt-account-id": true,
	"x-api-key": true, "api-key": true, "openai-organization": true, "openai-project": true, "forwarded": true,
	"x-forwarded-for": true, "x-forwarded-host": true, "x-forwarded-proto": true, "x-real-ip": true,
	"cf-connecting-ip": true, "true-client-ip": true,
	"x-openai-fedramp": true, "x-opencdx-device": true, "connection": true, "proxy-connection": true,
	"keep-alive": true, "transfer-encoding": true, "te": true, "trailer": true, "upgrade": true,
	"content-length": true,
}

func copyRequestHeaders(destination, source http.Header) {
	for name, values := range source {
		if requestBlockedHeaders[strings.ToLower(name)] {
			continue
		}
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

func copyResponseHeaders(destination, source http.Header) {
	for name, values := range source {
		lower := strings.ToLower(name)
		if lower == "connection" || lower == "proxy-connection" || lower == "keep-alive" || lower == "transfer-encoding" || lower == "trailer" || lower == "upgrade" || lower == "set-cookie" || strings.HasPrefix(lower, "x-opencdx-") {
			continue
		}
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

type streamCopyDirection uint8

const (
	streamCopyFromUpstream streamCopyDirection = iota
	streamCopyToDownstream
)

type streamCopyError struct {
	direction streamCopyDirection
	cause     error
}

func (copyError *streamCopyError) Error() string { return copyError.cause.Error() }
func (copyError *streamCopyError) Unwrap() error { return copyError.cause }

func copyStreaming(writer http.ResponseWriter, reader io.Reader) (int64, error) {
	buffer := make([]byte, 32<<10)
	var total int64
	for {
		count, readErr := reader.Read(buffer)
		if count > 0 {
			written, writeErr := writer.Write(buffer[:count])
			total += int64(written)
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
			if writeErr != nil {
				return total, &streamCopyError{direction: streamCopyToDownstream, cause: writeErr}
			}
			if written != count {
				return total, &streamCopyError{direction: streamCopyToDownstream, cause: io.ErrShortWrite}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, &streamCopyError{direction: streamCopyFromUpstream, cause: readErr}
		}
	}
}

func streamEndedNormally(ctx context.Context, collector *tailCollector, copyErr error) bool {
	if copyErr == nil || collector.terminalResponseSeen() || ctx.Err() != nil {
		return true
	}
	var typed *streamCopyError
	return errors.As(copyErr, &typed) && typed.direction == streamCopyToDownstream
}

type tailCollector struct {
	limit int
	data  []byte
}

func newTailCollector(limit int) *tailCollector { return &tailCollector{limit: limit} }

func (collector *tailCollector) Write(data []byte) (int, error) {
	collector.data = append(collector.data, data...)
	if len(collector.data) > collector.limit {
		collector.data = append([]byte(nil), collector.data[len(collector.data)-collector.limit:]...)
	}
	return len(data), nil
}

func (collector *tailCollector) usage() (int64, int64) {
	var document any
	if json.Unmarshal(collector.data, &document) == nil {
		return findUsage(document)
	}
	scanner := bufio.NewScanner(bytes.NewReader(collector.data))
	scanner.Buffer(make([]byte, 4096), collector.limit)
	var input, output int64
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if line == "" || line == "[DONE]" || json.Unmarshal([]byte(line), &document) != nil {
			continue
		}
		if candidateInput, candidateOutput := findUsage(document); candidateInput > 0 || candidateOutput > 0 {
			input, output = candidateInput, candidateOutput
		}
	}
	return input, output
}

func (collector *tailCollector) terminalResponseSeen() bool {
	trimmed := bytes.TrimSpace(collector.data)
	if len(trimmed) == 0 {
		return false
	}
	var document map[string]any
	if json.Unmarshal(trimmed, &document) == nil {
		// A complete non-streaming JSON response reached the client.
		return true
	}
	scanner := bufio.NewScanner(bytes.NewReader(collector.data))
	scanner.Buffer(make([]byte, 4096), collector.limit)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if line == "[DONE]" {
			return true
		}
		if json.Unmarshal([]byte(line), &document) != nil {
			continue
		}
		switch document["type"] {
		case "response.completed", "response.failed", "response.incomplete":
			return true
		}
	}
	return false
}

func findUsage(value any) (int64, int64) {
	switch typed := value.(type) {
	case map[string]any:
		if usage, ok := typed["usage"].(map[string]any); ok {
			input := number(usage["input_tokens"])
			output := number(usage["output_tokens"])
			if input > 0 || output > 0 {
				return input, output
			}
		}
		for _, child := range typed {
			if input, output := findUsage(child); input > 0 || output > 0 {
				return input, output
			}
		}
	case []any:
		for _, child := range typed {
			if input, output := findUsage(child); input > 0 || output > 0 {
				return input, output
			}
		}
	}
	return 0, 0
}

func number(value any) int64 {
	if value, ok := value.(float64); ok {
		return int64(value)
	}
	return 0
}

func quotaReset(headers http.Header) time.Time {
	for _, name := range []string{"x-ratelimit-reset-requests", "retry-after"} {
		value := headers.Get(name)
		if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
			return time.Now().UTC().Add(time.Duration(seconds) * time.Second)
		}
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func publicRouteError(err error) string {
	if errors.Is(err, ErrNoEligibleAccount) {
		return err.Error()
	}
	message := err.Error()
	for _, sensitive := range []string{"token", "credential", "authorization", "api key", "account id"} {
		if strings.Contains(strings.ToLower(message), sensitive) {
			return "provider authentication or routing failed"
		}
	}
	return message
}

func writeProxyError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{"type": code, "message": message}})
}

func max(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
