package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/helper"
	"github.com/Dodelidoo-Labs/open-cdx/internal/usagehistory"
)

func TestReconcileUsageScansLocallyAndSendsOnlyAggregateSnapshot(t *testing.T) {
	var received usagehistory.Snapshot
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/api/v1/telemetry/reconcile" || request.Header.Get("Authorization") != "Bearer device-secret" {
			http.Error(writer, "unexpected request", http.StatusUnauthorized)
			return
		}
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&received); err != nil {
			http.Error(writer, "invalid snapshot", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(usagehistory.Result{
			ReconciledAt: time.Now().UTC().Format(time.RFC3339), FilesScanned: received.FilesScanned,
			EventsImported: received.EventsImported, RowsImported: len(received.Rows),
		})
	}))
	defer server.Close()

	directory := t.TempDir()
	configPath := filepath.Join(directory, "helper.json")
	config := helper.Config{
		RouterURL: server.URL, DeviceID: "device", DeviceName: "Test Mac", ListenPort: helper.DefaultPort,
		CatalogPath: filepath.Join(directory, "catalog.json"),
	}
	if err := helper.SaveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(directory, "codex")
	sessions := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	rollout := strings.Join([]string{
		`{"timestamp":"2026-08-28T00:00:00Z","type":"session_meta","payload":{"model_provider":"opencdx"}}`,
		`{"timestamp":"2026-08-28T00:00:01Z","type":"turn_context","payload":{"turn_id":"turn","model":"openrouter/vendor/model"}}`,
		`{"timestamp":"2026-08-28T00:00:02Z","type":"response_item","payload":{"type":"message","content":"PRIVATE-CONVERSATION"}}`,
		`{"timestamp":"2026-08-28T00:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":12,"cached_input_tokens":4,"output_tokens":3,"reasoning_output_tokens":1,"total_tokens":15},"last_token_usage":{"input_tokens":12,"cached_input_tokens":4,"output_tokens":3,"reasoning_output_tokens":1,"total_tokens":15}}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(sessions, "rollout.jsonl"), []byte(rollout), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	secrets := memorySecretStore{"device-token": "device-secret"}
	var previewOutput bytes.Buffer
	if err := reconcileUsageToWithSecrets(configPath, []string{"--codex-home", codexHome, "--preview-json"}, &previewOutput, secrets); err != nil {
		t.Fatal(err)
	}
	var preview usagehistory.ReconciliationPreview
	if err := json.Unmarshal(previewOutput.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview %q: %v", previewOutput.String(), err)
	}
	if requests != 0 || preview.FilesScanned != 1 || preview.EventsImported != 1 || preview.RowsFound != 1 ||
		preview.RoutedRequests != 1 || preview.NativeRequests != 0 {
		t.Fatalf("preview = %#v, server requests = %d", preview, requests)
	}
	var dryRunOutput bytes.Buffer
	if err := reconcileUsageToWithSecrets(configPath, []string{"--codex-home", codexHome, "--dry-run"}, &dryRunOutput, secrets); err != nil {
		t.Fatal(err)
	}
	if requests != 0 || !strings.Contains(dryRunOutput.String(), "1 routed and 0 native requests") {
		t.Fatalf("dry-run output = %q, server requests = %d", dryRunOutput.String(), requests)
	}
	if err := reconcileUsageToWithSecrets(configPath, []string{"--codex-home", codexHome, "--json"}, &output, secrets); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("server requests after reconciliation = %d", requests)
	}
	if received.EventsImported != 1 || len(received.Rows) != 1 || received.Rows[0].Provider != "openrouter" ||
		received.Rows[0].Routing != usagehistory.RoutingRouted || received.Rows[0].CachedInputTokens != 4 {
		t.Fatalf("received snapshot = %#v", received)
	}
	wire, err := json.Marshal(received)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wire, []byte("PRIVATE-CONVERSATION")) {
		t.Fatal("conversation content was sent by the helper")
	}
	if !strings.Contains(output.String(), `"events_imported":1`) {
		t.Fatalf("unexpected command output %q", output.String())
	}
}

type memorySecretStore map[string]string

func (store memorySecretStore) Get(account string) (string, error) { return store[account], nil }
func (store memorySecretStore) Set(account, value string) error {
	store[account] = value
	return nil
}
func (store memorySecretStore) Delete(account string) error {
	delete(store, account)
	return nil
}
