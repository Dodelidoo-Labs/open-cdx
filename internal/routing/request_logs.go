package routing

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

// Only explicitly selected metadata reaches storage. Buffers are bounded even for long streams.
type logResponseWriter struct {
	http.ResponseWriter
	entry *storage.RequestLog
}

func (w *logResponseWriter) WriteHeader(status int) {
	if w.entry.Status == 0 {
		w.entry.Status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *logResponseWriter) Write(p []byte) (int, error) {
	if w.entry.Status == 0 {
		w.WriteHeader(200)
	}
	n, err := w.ResponseWriter.Write(p)
	w.entry.ResponseBytes += int64(n)
	return n, err
}
func (w *logResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *logResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (proxy *Proxy) startRequestLog(writer http.ResponseWriter, request *http.Request, device DeviceContext) (*logResponseWriter, func()) {
	id, err := secure.RandomURLSafe(18)
	if err != nil {
		slog.Error("request log ID generation failed")
		id = time.Now().UTC().Format("20060102T150405.000000000")
	}
	entry := &storage.RequestLog{ID: id, StartedAt: time.Now().UTC(), DeviceID: device.ID, Method: request.Method, Path: request.URL.Path,
		ThreadID: logText(request.Header.Get("thread-id"), 256), SessionID: logText(request.Header.Get("session-id"), 256), ClientVersion: logText(request.Header.Get("version"), 128), Attempts: []storage.RequestLogAttempt{}}
	writer.Header().Set("X-OpenCDX-Request-ID", id)
	wrapped := &logResponseWriter{ResponseWriter: writer, entry: entry}
	return wrapped, func() {
		entry.DurationMS = time.Since(entry.StartedAt).Milliseconds()
		if entry.Outcome == "" {
			switch {
			case request.Context().Err() != nil:
				entry.Outcome = "cancelled"
			case entry.Status >= 400 || entry.ErrorType != "" || entry.Status == 0:
				entry.Outcome = "error"
			default:
				entry.Outcome = "success"
			}
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), 2*time.Second)
		defer cancel()
		if err := proxy.store.RecordRequestLog(ctx, *entry); err != nil {
			slog.Error("request log could not be saved", "request_id", entry.ID, "error", err)
		}
	}
}

func requestLogMetadata(entry *storage.RequestLog, raw map[string]json.RawMessage) {
	get := func(key string) string {
		var value string
		_ = json.Unmarshal(raw[key], &value)
		return logText(value, 256)
	}
	entry.Model = get("model")
	entry.ServiceTier = get("service_tier")
	_ = json.Unmarshal(raw["stream"], &entry.Stream)
	var reasoning struct {
		Effort string `json:"effort"`
	}
	_ = json.Unmarshal(raw["reasoning"], &reasoning)
	entry.ReasoningEffort = logText(reasoning.Effort, 64)
}

func (proxy *Proxy) loggedAttempt(ctx context.Context, source *http.Request, target routeTarget, body []byte, entry *storage.RequestLog) (*http.Response, error) {
	start := time.Now()
	entry.Attempts = append(entry.Attempts, storage.RequestLogAttempt{AccountID: target.account.ID, Account: target.account.MaskedEmail})
	index := len(entry.Attempts) - 1
	response, err := proxy.attempt(ctx, source, target, body)
	attempt := &entry.Attempts[index]
	attempt.HeadersMS = time.Since(start).Milliseconds()
	if err != nil {
		attempt.DurationMS = attempt.HeadersMS
		attempt.ErrorType = "upstream_unavailable"
		attempt.ErrorMessage = "The provider connection failed"
		return response, err
	}
	attempt.Status = response.StatusCode
	attempt.RequestID = logText(response.Header.Get("x-request-id"), 256)
	if attempt.RequestID == "" {
		attempt.RequestID = logText(response.Header.Get("request-id"), 256)
	}
	response.Body = &logResponseBody{ReadCloser: response.Body, entry: entry, index: index, start: start, collector: newTailCollector(256 << 10)}
	return response, nil
}

type logResponseBody struct {
	io.ReadCloser
	entry     *storage.RequestLog
	index     int
	start     time.Time
	collector *tailCollector
	closed    bool
}

func (body *logResponseBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	if n > 0 {
		attempt := &body.entry.Attempts[body.index]
		if attempt.FirstByteMS == nil {
			ms := time.Since(body.start).Milliseconds()
			attempt.FirstByteMS = &ms
		}
		_, _ = body.collector.Write(p[:n])
	}
	return n, err
}
func (body *logResponseBody) Close() error {
	if !body.closed {
		body.closed = true
		body.entry.Attempts[body.index].DurationMS = time.Since(body.start).Milliseconds()
		body.collectMetadata()
		attempt := &body.entry.Attempts[body.index]
		if attempt.Status >= 400 && attempt.ErrorType == "" {
			attempt.ErrorType = "upstream_http_error"
			if attempt.ErrorMessage == "" {
				attempt.ErrorMessage = http.StatusText(attempt.Status)
			}
		}
		if attempt.Status >= 400 && body.entry.Status == attempt.Status && body.entry.ErrorType == "" {
			body.entry.ErrorType, body.entry.ErrorCode, body.entry.ErrorMessage = attempt.ErrorType, attempt.ErrorCode, attempt.ErrorMessage
		}
	}
	return body.ReadCloser.Close()
}
func (body *logResponseBody) collectMetadata() {
	consume := func(raw []byte) {
		var doc map[string]json.RawMessage
		if json.Unmarshal(raw, &doc) != nil {
			return
		}
		var kind string
		_ = json.Unmarshal(doc["type"], &kind)
		if response := doc["response"]; len(response) > 0 {
			var nested map[string]json.RawMessage
			if json.Unmarshal(response, &nested) == nil {
				doc = nested
			}
		}
		var id, model string
		_ = json.Unmarshal(doc["id"], &id)
		_ = json.Unmarshal(doc["model"], &model)
		if id != "" {
			body.entry.ResponseID = logText(id, 256)
		}
		if model != "" {
			body.entry.ResponseModel = logText(model, 256)
		}
		var usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			InputDetails struct {
				Cached int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputDetails struct {
				Reasoning int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		}
		if value := doc["usage"]; len(value) > 0 && string(value) != "null" && json.Unmarshal(value, &usage) == nil {
			body.entry.Usage = &storage.RequestLogUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CachedInputTokens: usage.InputDetails.Cached, ReasoningOutputTokens: usage.OutputDetails.Reasoning}
		}
		var failure struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(doc["error"], &failure)
		if kind == "error" {
			_ = json.Unmarshal(raw, &failure)
		}
		if failure.Type != "" || failure.Code != "" || failure.Message != "" {
			a := &body.entry.Attempts[body.index]
			a.ErrorType = logText(failure.Type, 128)
			a.ErrorCode = logText(failure.Code, 128)
			a.ErrorMessage = logText(failure.Message, 2048)
			body.entry.ErrorType = a.ErrorType
			if body.entry.ErrorType == "" {
				body.entry.ErrorType = "upstream_error"
			}
			body.entry.ErrorCode = a.ErrorCode
			body.entry.ErrorMessage = a.ErrorMessage
			body.entry.Outcome = "error"
		}
		var status string
		_ = json.Unmarshal(doc["status"], &status)
		if kind == "response.failed" || status == "failed" {
			body.entry.Outcome = "error"
			if body.entry.ErrorType == "" {
				body.entry.ErrorType = "response_failed"
			}
		}
		if kind == "response.incomplete" || status == "incomplete" {
			body.entry.Outcome = "incomplete"
			var details struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(doc["incomplete_details"], &details)
			body.entry.ErrorType = "response_incomplete"
			body.entry.ErrorCode = logText(details.Reason, 128)
		}
	}
	data := bytes.TrimSpace(body.collector.data)
	if json.Valid(data) {
		consume(data)
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), body.collector.limit)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if bytes.HasPrefix(line, []byte("data:")) {
			consume(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:"))))
		}
	}
}

var logSecretPattern = regexp.MustCompile(`(?i)(bearer\s+[^\s"',;]+|\bsk-[a-z0-9_-]+|(?:api[_ -]?key|access_token|refresh_token|authorization)\s*[=:]\s*[^\s,;]+)`)

func logText(value string, limit int) string {
	value = logSecretPattern.ReplaceAllString(value, "[redacted]")
	value = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, value)
	if len(value) > limit {
		return strings.ToValidUTF8(value[:limit], "") + "…"
	}
	return value
}
