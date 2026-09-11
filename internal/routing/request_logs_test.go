package routing

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func TestRequestLogsCaptureMetadataAndFailures(t *testing.T) {
	for _, test := range []struct {
		name                                  string
		status                                int
		body, contentType, outcome, errorCode string
		usage                                 bool
	}{
		{"success", 200, `{"id":"resp_123","model":"gpt-native","usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":80},"output_tokens":20,"output_tokens_details":{"reasoning_tokens":12}},"output":[{"text":"private-output"}]}`, "application/json", "success", "", true},
		{"http error", 502, `{"error":{"type":"server_error","code":"provider_failed","message":"Failure with Bearer secret-auth and sk-secret-key"}}`, "application/json", "error", "provider_failed", false},
		{"stream failure", 200, "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"stream_failed\",\"message\":\"upstream failure\"}}}\n\ndata: [DONE]\n\n", "text/event-stream", "error", "stream_failed", false},
		{"incomplete", 200, "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n", "text/event-stream", "incomplete", "max_output_tokens", false},
		{"truncated", 200, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"private-output\"}\n\n", "text/event-stream", "incomplete", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.Header().Set("X-Request-ID", "upstream-id")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer upstream.Close()
			proxy, store, _ := proxyFixture(t, upstream.Client(), upstream.URL, []routeFixture{{stable: "account", models: []string{"gpt-native"}}})
			request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-native","reasoning":{"effort":"high"},"stream":true,"input":"private-prompt","instructions":"private-instructions"}`))
			request.Header.Set("Authorization", "Bearer secret-device")
			request.Header.Set("Thread-Id", "thread-123")
			writer := httptest.NewRecorder()
			proxy.ServeDeviceHTTP(writer, request, DeviceContext{ID: "machine"})
			page, err := store.RequestLogs(context.Background(), storage.RequestLogFilter{})
			if err != nil || len(page.Logs) != 1 {
				t.Fatalf("logs: %#v %v", page, err)
			}
			log := page.Logs[0]
			if log.Status != test.status || log.Outcome != test.outcome || log.ErrorCode != test.errorCode || log.ReasoningEffort != "high" || log.ThreadID != "thread-123" || log.RequestBytes == 0 || log.ResponseBytes == 0 {
				t.Fatalf("metadata: %#v", log)
			}
			if len(log.Attempts) != 1 || log.Attempts[0].RequestID != "upstream-id" || log.Attempts[0].FirstByteMS == nil {
				t.Fatalf("attempts: %#v", log.Attempts)
			}
			if test.usage && (log.Usage == nil || log.Usage.InputTokens != 100 || log.Usage.CachedInputTokens != 80 || log.Usage.ReasoningOutputTokens != 12) {
				t.Fatalf("usage: %#v", log.Usage)
			}
			if !test.usage && log.Usage != nil {
				t.Fatal("missing usage reported as zero")
			}
			raw, _ := json.Marshal(log)
			for _, secret := range []string{"private-prompt", "private-output", "private-instructions", "secret-auth", "sk-secret-key", "secret-device"} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("log leaked %s", secret)
				}
			}
			if writer.Header().Get("X-OpenCDX-Request-ID") != log.ID {
				t.Fatal("correlation ID missing")
			}
		})
	}
}
func TestRequestLogRecordsValidationAndRouteFailure(t *testing.T) {
	proxy, store, _ := proxyFixture(t, &http.Client{}, "https://unused.invalid", nil)
	for _, body := range []string{`bad json`, `{}`, `{"model":"gpt-missing"}`} {
		proxy.ServeDeviceHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body)), DeviceContext{ID: "device"})
	}
	page, err := store.RequestLogs(context.Background(), storage.RequestLogFilter{})
	if err != nil || len(page.Logs) != 3 {
		t.Fatalf("early failures missing: %#v %v", page, err)
	}
	for _, entry := range page.Logs {
		if entry.Outcome != "error" || entry.ErrorType == "" || entry.Status < 400 {
			t.Fatalf("failure metadata: %#v", entry)
		}
	}
}

func TestRequestLogsKeepRetryAttempts(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		status, body := 429, `{"error":{"code":"rate_limit"}}`
		if request.Header.Get("ChatGPT-Account-ID") == "second" {
			status, body = 200, `{"id":"ok"}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	proxy, store, _ := proxyFixture(t, &http.Client{Transport: transport}, "https://upstream.invalid", []routeFixture{{stable: "first", quota: 1, models: []string{"gpt-native"}}, {stable: "second", quota: 50, models: []string{"gpt-native"}}})
	proxy.ServeDeviceHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-native"}`)), DeviceContext{ID: "device"})
	page, err := store.RequestLogs(context.Background(), storage.RequestLogFilter{})
	if err != nil || len(page.Logs) != 1 {
		t.Fatalf("logs: %#v %v", page, err)
	}
	log := page.Logs[0]
	if log.Outcome != "success" || len(log.Attempts) != 2 || log.Attempts[0].Status != 429 || log.Attempts[1].Status != 200 || log.Attempts[0].AccountID == log.Attempts[1].AccountID {
		t.Fatalf("retry log: %#v", log)
	}
}
func TestRequestLogsPersistAfterClientDisconnect(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: partial\n\n")), Request: request}, nil
	})
	proxy, store, _ := proxyFixture(t, &http.Client{Transport: transport}, "https://upstream.invalid", []routeFixture{{stable: "account", models: []string{"gpt-native"}}})
	proxy.ServeDeviceHTTP(&failingResponseWriter{header: make(http.Header)}, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-native"}`)), DeviceContext{ID: "device"})
	page, err := store.RequestLogs(context.Background(), storage.RequestLogFilter{})
	if err != nil || len(page.Logs) != 1 || page.Logs[0].Outcome != "cancelled" || page.Logs[0].ErrorType != "client_disconnected" {
		t.Fatalf("disconnect log: %#v %v", page, err)
	}
}

func TestResponseTailRemainsBounded(t *testing.T) {
	collector := newTailCollector(32)
	var all string
	for _, chunk := range []string{"hello", strings.Repeat("x", 100), "suffix", strings.Repeat("y", 24), "done"} {
		n, err := collector.Write([]byte(chunk))
		if err != nil || n != len(chunk) {
			t.Fatal("short collector write")
		}
		all += chunk
		want := all
		if len(want) > 32 {
			want = want[len(want)-32:]
		}
		if string(collector.data) != want || cap(collector.data) > 32 {
			t.Fatalf("unbounded or incorrect tail: %q capacity %d", collector.data, cap(collector.data))
		}
	}
}
