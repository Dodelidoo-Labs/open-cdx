package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/claudecode"
	"github.com/Dodelidoo-Labs/open-cdx/internal/helper"
)

func claudeTestConfig(t *testing.T, routerURL string, port int) string {
	t.Helper()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "helper.json")
	if routerURL == "" {
		routerURL = "https://router.example.com"
	}
	if err := helper.SaveConfig(configPath, helper.Config{RouterURL: routerURL, DeviceID: "device", ListenPort: port, CatalogPath: filepath.Join(directory, "catalog.json")}); err != nil {
		t.Fatal(err)
	}
	secrets := filepath.Join(directory, "secrets.json")
	if err := os.WriteFile(secrets, []byte(`{"local-token-secret":"012345678901234567890123456789012345","device-token":"device-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(helper.SecretFileEnvironment, secrets)
	return configPath
}

func TestClaudeSetupPreviewsAppliesAndRestoresSettings(t *testing.T) {
	configPath := claudeTestConfig(t, "", helper.DefaultPort)
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	original := "{\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"echo mine\"\n  },\n  \"theme\": \"dark\"\n}\n"
	if err := os.WriteFile(settingsPath, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	args := []string{"--settings", settingsPath, "--helper", "/Applications/OpenCDX Router.app/Contents/Resources/router-helper"}
	var output bytes.Buffer
	if err := claudeSetupTo(configPath, append(args, "--preview-json"), &output); err != nil {
		t.Fatal(err)
	}
	var preview claudeSetupPreview
	if err := json.Unmarshal(output.Bytes(), &preview); err != nil || preview.Installed || preview.AlreadyApplied || preview.WrapsStatusLine != "echo mine" || len(preview.Changes) != 3 {
		t.Fatalf("preview = %#v %v", preview, err)
	}
	if current, _ := os.ReadFile(settingsPath); string(current) != original {
		t.Fatal("preview changed the settings file")
	}
	if err := claudeSetupTo(configPath, append(args, "--apply"), &output); err != nil {
		t.Fatal(err)
	}
	applied, _ := os.ReadFile(settingsPath)
	if !strings.Contains(string(applied), `"'/Applications/OpenCDX Router.app/Contents/Resources/router-helper' --config `) || !strings.Contains(string(applied), "claude-statusline") {
		t.Fatalf("applied settings = %s", applied)
	}
	if info, _ := os.Stat(settingsPath); info.Mode().Perm() != 0o640 {
		t.Fatalf("settings mode = %v", info.Mode().Perm())
	}
	if backup, _ := os.ReadFile(settingsPath + ".opencdx-backup"); string(backup) != original {
		t.Fatalf("backup = %q", backup)
	}
	if command := wrappedStatusLineCommand(configPath); command != "echo mine" {
		t.Fatalf("wrapped command = %q", command)
	}
	// Reapplying, including after the app moves, keeps the original status line.
	moved := []string{"--settings", settingsPath, "--helper", "/Users/someone/Applications/OpenCDX Router.app/Contents/Resources/router-helper", "--apply"}
	if err := claudeSetupTo(configPath, moved, &output); err != nil {
		t.Fatal(err)
	}
	if command := wrappedStatusLineCommand(configPath); command != "echo mine" {
		t.Fatalf("wrapped command after update = %q", command)
	}
	if err := claudeSetupTo(configPath, []string{"--settings", settingsPath, "--remove", "--apply"}, &output); err != nil {
		t.Fatal(err)
	}
	if restored, _ := os.ReadFile(settingsPath); string(restored) != original {
		t.Fatalf("restored settings = %s", restored)
	}
	if _, err := os.Stat(claudeStatePath(configPath)); !os.IsNotExist(err) {
		t.Fatalf("state file remained: %v", err)
	}
}

func TestClaudeSetupReportsConflictsWithoutWriting(t *testing.T) {
	configPath := claudeTestConfig(t, "", helper.DefaultPort)
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	original := `{"env":{"OTEL_EXPORTER_OTLP_ENDPOINT":"https://collector.example.com"}}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := claudeSetupTo(configPath, []string{"--settings", settingsPath, "--preview-json"}, &output); err != nil {
		t.Fatal(err)
	}
	var preview claudeSetupPreview
	if err := json.Unmarshal(output.Bytes(), &preview); err != nil || len(preview.Conflicts) != 1 {
		t.Fatalf("preview = %s", output.String())
	}
	if err := claudeSetupTo(configPath, []string{"--settings", settingsPath, "--apply"}, &output); err == nil {
		t.Fatal("conflicting settings were overwritten")
	}
	if current, _ := os.ReadFile(settingsPath); string(current) != original {
		t.Fatal("conflicting settings changed")
	}
}

func TestStatusLineReportsChangedAllowanceOnce(t *testing.T) {
	var posts atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/claude/statusline" || !helper.VerifyLocalToken("012345678901234567890123456789012345", strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "), time.Now().Add(time.Minute)) {
			http.Error(writer, "unexpected", http.StatusUnauthorized)
			return
		}
		var upload helper.StatusLineUpload
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&upload); err != nil || upload.SessionID != "session" || len(upload.Windows) != 1 {
			http.Error(writer, "invalid", http.StatusBadRequest)
			return
		}
		posts.Add(1)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer daemon.Close()
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(daemon.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	configPath := claudeTestConfig(t, "", port)
	now := time.Now().UTC()
	input := func(used int) []byte {
		return []byte(`{"session_id":"session","cwd":"/private/path","rate_limits":{"five_hour":{"used_percentage":` + strconv.Itoa(used) + `,"resets_at":` + strconv.FormatInt(now.Add(time.Hour).Unix(), 10) + `}}}`)
	}
	for _, reading := range [][]byte{input(5), input(5), input(6), []byte(`{"session_id":"api-key-user"}`)} {
		if err := reportStatusLine(context.Background(), configPath, reading, now); err != nil {
			t.Fatal(err)
		}
	}
	if posts.Load() != 2 {
		t.Fatalf("posts = %d", posts.Load())
	}
	if err := reportStatusLine(context.Background(), configPath, input(6), now.Add(2*time.Minute)); err != nil || posts.Load() != 3 {
		t.Fatalf("periodic repeat posts = %d %v", posts.Load(), err)
	}
}

func TestClaudeImportSendsOnlyUsageCounters(t *testing.T) {
	var received claudecode.Report
	router := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if request.URL.Path != "/api/v1/claude/telemetry" || request.Header.Get("Authorization") != "Bearer device-secret" || decoder.Decode(&received) != nil {
			http.Error(writer, "unexpected", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(claudecode.Result{RequestsAdded: 1})
	}))
	defer router.Close()
	configPath := claudeTestConfig(t, router.URL, helper.DefaultPort)
	home := t.TempDir()
	project := filepath.Join(home, "projects", "p")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"user","message":{"content":"PRIVATE PROMPT"},"timestamp":"2026-09-29T10:00:00Z"}
{"type":"assistant","requestId":"req_1","timestamp":"2026-09-29T10:00:01Z","cwd":"/private/path","message":{"model":"claude-opus-5-5","content":[{"type":"text","text":"PRIVATE ANSWER"}],"usage":{"input_tokens":1,"output_tokens":2}}}
`
	if err := os.WriteFile(filepath.Join(project, "s.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	secrets := helper.NewSecretStore(configPath)
	if err := claudeImportTo(configPath, []string{"--claude-home", home, "--dry-run"}, &output, secrets); err != nil || !strings.Contains(output.String(), "Found 1 Claude Code requests") {
		t.Fatalf("dry run = %q %v", output.String(), err)
	}
	output.Reset()
	if err := claudeImportTo(configPath, []string{"--claude-home", home}, &output, secrets); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(received)
	if received.Source != claudecode.SourceHistory || len(received.Requests) != 1 || strings.Contains(string(encoded), "PRIVATE") || !strings.Contains(output.String(), "1 new requests") {
		t.Fatalf("import sent %s, output %q", encoded, output.String())
	}
	if err := claudeImportTo(configPath, []string{"--claude-home", t.TempDir()}, &output, secrets); err == nil {
		t.Fatal("empty Claude home was accepted")
	}
}
