package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/claudecode"
	"github.com/Dodelidoo-Labs/open-cdx/internal/helper"
)

const (
	claudeStatusLineCacheFile = "claude-statusline-cache.json"
	statusLineRepeatInterval  = time.Minute
	historyUploadBatch        = 50_000
)

// claudeState remembers the status line that setup wrapped, so the wrapper can
// run it and removal can restore it exactly.
type claudeState struct {
	SettingsPath      string          `json:"settings_path"`
	WrappedStatusLine json.RawMessage `json:"wrapped_status_line,omitempty"`
}

func claudeStatePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), helper.ClaudeStateFile)
}

func loadClaudeState(configPath string) (claudeState, error) {
	var state claudeState
	raw, err := os.ReadFile(claudeStatePath(configPath))
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(raw, &state)
	return state, err
}

// claudeStatusLine runs as Claude Code's status line command. It prints the
// wrapped status line's output unchanged and reports plan allowance to the
// local daemon. Reporting failures never affect the displayed status line.
func claudeStatusLine(configPath string, args []string) error {
	flags := flag.NewFlagSet(claudecode.StatusLineSubcommand, flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		reportCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = reportStatusLine(reportCtx, configPath, input, time.Now().UTC())
	}()
	exitCode := 0
	if command := wrappedStatusLineCommand(configPath); command != "" {
		process := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		process.Stdin, process.Stdout, process.Stderr = bytes.NewReader(input), os.Stdout, os.Stderr
		if err := process.Run(); err != nil {
			exitCode = 1
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
				exitCode = exitErr.ExitCode()
			}
		}
	}
	group.Wait()
	if exitCode != 0 {
		stop()
		os.Exit(exitCode)
	}
	return nil
}

func wrappedStatusLineCommand(configPath string) string {
	state, err := loadClaudeState(configPath)
	if err != nil || len(state.WrappedStatusLine) == 0 {
		return ""
	}
	var original struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	if json.Unmarshal(state.WrappedStatusLine, &original) != nil || original.Type != "command" {
		return ""
	}
	return original.Command
}

type statusLineCache struct {
	SessionID string              `json:"session_id"`
	Windows   []claudecode.Window `json:"windows"`
	SentAt    time.Time           `json:"sent_at"`
}

func reportStatusLine(ctx context.Context, configPath string, input []byte, now time.Time) error {
	reading, err := claudecode.ParseStatusLine(input, now)
	if err != nil || len(reading.Windows) == 0 {
		return err
	}
	cachePath := filepath.Join(filepath.Dir(configPath), claudeStatusLineCacheFile)
	var cache statusLineCache
	if raw, readErr := os.ReadFile(cachePath); readErr == nil && json.Unmarshal(raw, &cache) == nil &&
		cache.SessionID == reading.SessionID && sameWindows(cache.Windows, reading.Windows) && now.Sub(cache.SentAt) < statusLineRepeatInterval {
		return nil
	}
	config, err := helper.LoadConfig(configPath)
	if err != nil {
		return err
	}
	secret, err := helper.NewSecretStore(configPath).Get("local-token-secret")
	if err != nil {
		return err
	}
	token, err := helper.IssueLocalToken(secret, now)
	if err != nil {
		return err
	}
	upload := helper.StatusLineUpload{SessionID: reading.SessionID, ObservedAt: now, Windows: reading.Windows}
	if account, ok := claudecode.LocalAccount(); ok {
		upload.Account = &account
	}
	body, _ := json.Marshal(upload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.LocalBaseURL()+"/claude/statusline", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("helper returned %s", response.Status)
	}
	encoded, _ := json.Marshal(statusLineCache{SessionID: reading.SessionID, Windows: reading.Windows, SentAt: now})
	return helper.AtomicWrite(cachePath, encoded, 0o600)
}

func sameWindows(left, right []claudecode.Window) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// claudeOTelHeaders is Claude Code's otelHeadersHelper. It prints a local
// credential that authenticates only the daemon's telemetry endpoint.
func claudeOTelHeaders(configPath string, args []string) error {
	flags := flag.NewFlagSet(claudecode.HeadersSubcommand, flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	secret, err := helper.NewSecretStore(configPath).Get("local-token-secret")
	if err != nil {
		return errors.New("local helper token is unavailable")
	}
	token, err := helper.IssueScopedToken(secret, helper.ClaudeOTLPScope, time.Now().UTC())
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"Authorization": "Bearer " + token})
}

type claudeSetupPreview struct {
	SettingsPath     string   `json:"settings_path"`
	Action           string   `json:"action"`
	Installed        bool     `json:"installed"`
	AlreadyApplied   bool     `json:"already_applied"`
	Changes          []string `json:"changes"`
	Conflicts        []string `json:"conflicts,omitempty"`
	WrapsStatusLine  string   `json:"wraps_status_line,omitempty"`
	RestartReminder  string   `json:"restart_reminder,omitempty"`
	HelperExecutable string   `json:"helper_executable"`
}

// claudeSetup previews or applies the OpenCDX entries in Claude Code's user
// settings. Nothing is written without --apply.
func claudeSetup(configPath string, args []string) error {
	return claudeSetupTo(configPath, args, os.Stdout)
}

func claudeSetupTo(configPath string, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("claude-setup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	settingsPath := flags.String("settings", "", "Claude Code user settings file (defaults to ~/.claude/settings.json)")
	remove := flags.Bool("remove", false, "remove the OpenCDX entries and restore the original status line")
	apply := flags.Bool("apply", false, "write the settings file")
	previewJSON := flags.Bool("preview-json", false, "print the proposed change as JSON")
	executableOverride := flags.String("helper", "", "router-helper path to reference (defaults to this executable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *settingsPath == "" {
		home, err := claudecode.DefaultHome()
		if err != nil {
			return err
		}
		*settingsPath = filepath.Join(home, "settings.json")
	}
	if !filepath.IsAbs(*settingsPath) {
		return errors.New("--settings must be an absolute path")
	}
	target, err := filepath.EvalSymlinks(*settingsPath)
	if errors.Is(err, fs.ErrNotExist) {
		target, err = *settingsPath, nil
	}
	if err != nil {
		return err
	}
	current, err := os.ReadFile(target)
	mode := fs.FileMode(0o600)
	if info, statErr := os.Stat(target); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	executable := *executableOverride
	if executable == "" {
		if executable, err = helper.ExecutablePath(); err != nil {
			return err
		}
	}
	invocation := claudecode.ShellQuote(executable)
	if defaultPath, defaultErr := helper.DefaultConfigPath(); defaultErr != nil || filepath.Clean(defaultPath) != filepath.Clean(configPath) {
		invocation += " --config " + claudecode.ShellQuote(configPath)
	}
	installed, err := claudecode.Installed(current)
	if err != nil {
		return fmt.Errorf("%s: %w", target, err)
	}
	preview := claudeSetupPreview{SettingsPath: target, Action: "install", Installed: installed, HelperExecutable: executable}
	var plan claudecode.Plan
	if *remove {
		preview.Action = "remove"
		state, _ := loadClaudeState(configPath)
		plan, err = claudecode.Remove(current, state.WrappedStatusLine)
	} else {
		config, configErr := helper.LoadConfig(configPath)
		if configErr != nil {
			return errors.New("pair this Mac with the router before connecting Claude Code")
		}
		plan, err = claudecode.Install(current, claudecode.Integration{HelperCommand: invocation, Port: config.ListenPort})
		preview.RestartReminder = "Start a new Claude Code session to begin exporting usage."
	}
	var conflict *claudecode.ConflictError
	if errors.As(err, &conflict) {
		preview.Conflicts = conflict.Conflicts
	} else if err != nil {
		return fmt.Errorf("%s: %w", target, err)
	}
	preview.Changes, preview.AlreadyApplied, preview.WrapsStatusLine = plan.Changes, plan.AlreadyInstalled && conflict == nil, plan.WrapsStatusLine
	if preview.Changes == nil {
		preview.Changes = []string{}
	}
	if !*apply {
		if *previewJSON {
			return json.NewEncoder(output).Encode(preview)
		}
		if conflict != nil {
			return conflict
		}
		if preview.AlreadyApplied {
			fmt.Fprintf(output, "%s is already connected; nothing to change.\n", target)
			return nil
		}
		fmt.Fprintf(output, "Proposed changes to %s:\n", target)
		for _, change := range preview.Changes {
			fmt.Fprintf(output, "  - %s\n", change)
		}
		fmt.Fprintln(output, "Run again with --apply to write them.")
		return nil
	}
	if conflict != nil {
		return conflict
	}
	if !preview.AlreadyApplied {
		if len(current) > 0 {
			if err = helper.AtomicWrite(target+".opencdx-backup", current, 0o600); err != nil {
				return err
			}
		}
		if err = helper.AtomicWrite(target, plan.Settings, mode); err != nil {
			return err
		}
	}
	statePath := claudeStatePath(configPath)
	if *remove {
		if err = os.Remove(statePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	} else if previous, stateErr := loadClaudeState(configPath); plan.Original != nil || stateErr != nil {
		// Updating an existing OpenCDX entry keeps the originally wrapped line.
		state := claudeState{SettingsPath: target, WrappedStatusLine: plan.Original}
		if plan.Original == nil && stateErr == nil {
			state.WrappedStatusLine = previous.WrappedStatusLine
		}
		encoded, _ := json.MarshalIndent(state, "", "  ")
		if err = helper.AtomicWrite(statePath, encoded, 0o600); err != nil {
			return err
		}
	}
	if *previewJSON {
		return json.NewEncoder(output).Encode(preview)
	}
	if *remove {
		fmt.Fprintf(output, "Removed OpenCDX from %s.\n", target)
	} else {
		fmt.Fprintf(output, "Connected Claude Code in %s. %s\n", target, preview.RestartReminder)
	}
	return nil
}

// claudeImport adds usage from local Claude Code transcripts. Requests already
// recorded, by live telemetry or an earlier import, are skipped by the router.
func claudeImport(configPath string, args []string) error {
	return claudeImportTo(configPath, args, os.Stdout, helper.NewSecretStore(configPath))
}

func claudeImportTo(configPath string, args []string, output io.Writer, secrets helper.SecretStore) error {
	flags := flag.NewFlagSet("claude-import", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	claudeHome := flags.String("claude-home", "", "Claude Code data directory (defaults to CLAUDE_CONFIG_DIR or ~/.claude)")
	previewJSON := flags.Bool("preview-json", false, "print a JSON summary without uploading")
	dryRun := flags.Bool("dry-run", false, "print a summary without uploading")
	jsonOutput := flags.Bool("json", false, "print the import result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *claudeHome == "" {
		var err error
		if *claudeHome, err = claudecode.DefaultHome(); err != nil {
			return err
		}
	}
	history, err := claudecode.ScanHistory(context.Background(), *claudeHome)
	if err != nil {
		return err
	}
	if len(history.Requests) == 0 {
		return errors.New("no Claude Code usage was found in local transcripts; telemetry was left unchanged")
	}
	if *previewJSON {
		return json.NewEncoder(output).Encode(history.Preview())
	}
	if *dryRun {
		fmt.Fprintf(output, "Found %d Claude Code requests across %d models in %d transcript files. Prompts and responses were not read or exported.\n",
			len(history.Requests), history.Models, history.FilesScanned)
		return nil
	}
	_, client, err := pairedClientWithSecrets(configPath, secrets)
	if err != nil {
		return err
	}
	var total claudecode.Result
	for start := 0; start < len(history.Requests); start += historyUploadBatch {
		end := min(start+historyUploadBatch, len(history.Requests))
		report := claudecode.Report{Version: claudecode.ReportVersion, Source: claudecode.SourceHistory, Requests: history.Requests[start:end], FilesScanned: history.FilesScanned}
		var result claudecode.Result
		if _, err = client.JSON(context.Background(), http.MethodPost, "/api/v1/claude/telemetry", report, &result, true); err != nil {
			return fmt.Errorf("imported %d requests before failing: %w", total.RequestsAdded, err)
		}
		total.RequestsAdded += result.RequestsAdded
		total.RequestsDuplicated += result.RequestsDuplicated
	}
	if *jsonOutput {
		return json.NewEncoder(output).Encode(total)
	}
	fmt.Fprintf(output, "Claude Code history imported: %d new requests, %d already recorded. Prompts and responses were not imported.\n",
		total.RequestsAdded, total.RequestsDuplicated)
	return nil
}
