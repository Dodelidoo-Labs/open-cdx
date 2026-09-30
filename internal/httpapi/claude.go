package httpapi

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/claudecode"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

const (
	maxLiveClaudeRequests    = 20_000
	maxHistoryClaudeRequests = 5_000_000
	maxClaudeAllowances      = 1_000
)

// claudeTelemetry stores usage and plan allowance observed by a paired Mac's
// helper. Claude Code talks to Anthropic directly; this endpoint receives only
// counters, model IDs, opaque request IDs, and masked account labels.
func (server *Server) claudeTelemetry(writer http.ResponseWriter, request *http.Request) {
	var report claudecode.Report
	if !decodeJSONLimit(writer, request, &report, 512<<20) {
		return
	}
	now := time.Now().UTC()
	requests, allowances, err := validatedClaudeReport(report, now)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_claude_telemetry", err.Error())
		return
	}
	ctx := request.Context()
	device := currentDevice(ctx)
	masked := make(map[string]string, len(report.Accounts))
	for _, account := range report.Accounts {
		masked[account.Identity] = account.MaskedEmail
	}
	accountIDs := make(map[string]string)
	resolve := func(identity string) (string, error) {
		if identity == "" {
			// Without Claude Code's OpenTelemetry account attributes, an
			// allowance can only be attributed to the reporting machine.
			identity = machineClaudeIdentity(device.ID)
		}
		if id, ok := accountIDs[identity]; ok {
			return id, nil
		}
		id, err := server.store.EnsureClaudeAccount(ctx, identity, masked[identity], device.ID)
		if err == nil {
			accountIDs[identity] = id
		}
		return id, err
	}
	stored := make([]storage.ClaudeRequest, 0, len(requests))
	for _, row := range requests {
		accountID := ""
		if row.Account != "" {
			if accountID, err = resolve(row.Account); err != nil {
				writeAPIError(writer, http.StatusInternalServerError, "claude_telemetry_failed", "Claude Code telemetry could not be stored")
				return
			}
		}
		recorded, _ := time.Parse(time.RFC3339Nano, row.At)
		stored = append(stored, storage.ClaudeRequest{
			RequestID: row.ID, RecordedAt: recorded, Model: row.Model, AccountID: accountID,
			// OpenCDX counts cached input as part of input, like OpenAI usage.
			InputTokens:       row.InputTokens + row.CacheReadTokens + row.CacheCreationTokens,
			CachedInputTokens: row.CacheReadTokens, CacheWriteInputTokens: row.CacheCreationTokens,
			OutputTokens: row.OutputTokens, ReasoningOutputTokens: row.ThinkingTokens,
		})
	}
	source := storage.UsageSourceRouted
	if report.Source == claudecode.SourceHistory {
		source = storage.UsageSourceReconciled
	}
	added, err := server.store.RecordClaudeRequests(ctx, device.ID, source, stored)
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "claude_telemetry_failed", "Claude Code telemetry could not be stored")
		return
	}
	result := claudecode.Result{RequestsAdded: added, RequestsDuplicated: len(stored) - added}
	for _, allowance := range allowances {
		accountID, err := resolve(allowance.account)
		if err == nil {
			err = server.store.RecordClaudeAllowance(ctx, accountID, allowance.observedAt, allowance.windows)
		}
		if err == nil && allowance.account != "" {
			// Readings this Mac sent before its account was known belong to a
			// placeholder that would duplicate the identified subscription.
			err = server.store.DeleteClaudeAccountIdentity(ctx, machineClaudeIdentity(device.ID))
		}
		if err != nil {
			writeAPIError(writer, http.StatusInternalServerError, "claude_telemetry_failed", "Claude Code allowance could not be stored")
			return
		}
		result.AllowancesRecorded++
	}
	writeJSON(writer, http.StatusOK, result)
}

type claudeAllowance struct {
	account    string
	observedAt time.Time
	windows    []storage.ClaudeWindow
}

func validatedClaudeReport(report claudecode.Report, now time.Time) ([]claudecode.Request, []claudeAllowance, error) {
	if report.Version != claudecode.ReportVersion {
		return nil, nil, errors.New("Claude Code telemetry format is unsupported")
	}
	limit := maxLiveClaudeRequests
	switch report.Source {
	case claudecode.SourceLive:
	case claudecode.SourceHistory:
		limit = maxHistoryClaudeRequests
		if len(report.Allowances) > 0 {
			return nil, nil, errors.New("imported history cannot contain allowance readings")
		}
	default:
		return nil, nil, errors.New("Claude Code telemetry source is invalid")
	}
	if len(report.Requests) > limit || len(report.Allowances) > maxClaudeAllowances || len(report.Accounts) > 1_000 {
		return nil, nil, errors.New("Claude Code telemetry is too large")
	}
	accounts := make(map[string]struct{}, len(report.Accounts))
	for _, account := range report.Accounts {
		if !claudecode.ValidIdentity(account.Identity) || len(account.MaskedEmail) > 320 || strings.ContainsAny(account.MaskedEmail, "\x00\r\n") {
			return nil, nil, errors.New("Claude Code telemetry contains an invalid account")
		}
		accounts[account.Identity] = struct{}{}
	}
	knownAccount := func(identity string) bool {
		if identity == "" {
			return true
		}
		_, ok := accounts[identity]
		return ok
	}
	seen := make(map[string]struct{}, len(report.Requests))
	for _, row := range report.Requests {
		if row.ID == "" || len(row.ID) > 256 || strings.ContainsAny(row.ID, "\x00\r\n") {
			return nil, nil, errors.New("Claude Code telemetry contains an invalid request ID")
		}
		if _, duplicate := seen[row.ID]; duplicate {
			return nil, nil, errors.New("Claude Code telemetry contains duplicate requests")
		}
		seen[row.ID] = struct{}{}
		model := strings.TrimSpace(row.Model)
		if model == "" || model != row.Model || len(model) > 256 || strings.ContainsAny(model, "\x00\r\n") {
			return nil, nil, errors.New("Claude Code telemetry contains an invalid model")
		}
		at, err := time.Parse(time.RFC3339Nano, row.At)
		if err != nil || at.After(now.Add(5*time.Minute)) || at.Year() < 2020 {
			return nil, nil, errors.New("Claude Code telemetry contains an invalid timestamp")
		}
		if !knownAccount(row.Account) {
			return nil, nil, errors.New("Claude Code telemetry references an unknown account")
		}
		for _, count := range []int64{row.InputTokens, row.CacheReadTokens, row.CacheCreationTokens, row.OutputTokens, row.ThinkingTokens} {
			if !validUsageCount(count) {
				return nil, nil, errors.New("Claude Code telemetry contains invalid counters")
			}
		}
	}
	allowances := make([]claudeAllowance, 0, len(report.Allowances))
	for _, row := range report.Allowances {
		if !knownAccount(row.Account) || len(row.Windows) == 0 || len(row.Windows) > 8 {
			return nil, nil, errors.New("Claude Code telemetry contains an invalid allowance")
		}
		observed, err := time.Parse(time.RFC3339Nano, row.ObservedAt)
		if err != nil || observed.After(now.Add(5*time.Minute)) || observed.Before(now.Add(-31*24*time.Hour)) {
			return nil, nil, errors.New("Claude Code telemetry contains an invalid allowance time")
		}
		allowance := claudeAllowance{account: row.Account, observedAt: observed.UTC()}
		durations := make(map[int64]struct{}, len(row.Windows))
		for _, window := range row.Windows {
			reset, err := time.Parse(time.RFC3339Nano, window.ResetAt)
			if _, repeated := durations[window.Seconds]; err != nil || repeated || window.Seconds < 60 || window.Seconds > 366*86400 ||
				math.IsNaN(window.UsedPercent) || window.UsedPercent < 0 || window.UsedPercent > 100 {
				return nil, nil, errors.New("Claude Code telemetry contains an invalid allowance window")
			}
			durations[window.Seconds] = struct{}{}
			allowance.windows = append(allowance.windows, storage.ClaudeWindow{WindowSeconds: window.Seconds, UsedPercent: window.UsedPercent, ResetAt: reset.UTC()})
		}
		allowances = append(allowances, allowance)
	}
	return report.Requests, allowances, nil
}

// claudeAccountViews reports each observed subscription's latest windows in
// the same shape as OpenAI account allowances. A window whose reset time has
// passed shows as fully available until Claude Code reports again.
func claudeAccountViews(accounts []storage.ClaudeAccount, now time.Time) []map[string]any {
	result := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		windows := make([]quotaWindowState, 0, len(account.Windows))
		for _, index := range claudeWindowOrder(account.Windows) {
			window := account.Windows[index]
			quota := openai.QuotaWindow{UsedPercent: window.UsedPercent, Duration: time.Duration(window.WindowSeconds) * time.Second, ResetAt: window.ResetAt}
			state := quotaWindowState{Label: quota.Label(), Remaining: quota.RemainingPercent(), DurationMinutes: window.WindowSeconds / 60, ResetAt: window.ResetAt}
			if !now.Before(window.ResetAt) {
				state.Remaining, state.ResetAt = 100, time.Time{}
			} else if pace := quota.Pace(now); pace.Available {
				state.PaceStatus, state.PaceMarkerPercent, state.PaceBufferPercent = pace.Status, pace.RequiredRemainingPercent, pace.BufferPercent
			}
			windows = append(windows, state)
		}
		label := account.MaskedEmail
		if label == "" {
			label = "Claude subscription"
			if account.LastDeviceName != "" {
				label = fmt.Sprintf("Claude on %s", account.LastDeviceName)
			}
		}
		result = append(result, map[string]any{
			"id": account.ID, "masked_email": label, "observed_at": browserTimestamp(account.ObservedAt),
			"device_name": account.LastDeviceName, "quota_windows": liveQuotaWindows(windows),
		})
	}
	return result
}

func machineClaudeIdentity(deviceID string) string {
	return claudecode.AccountIdentity("device:" + deviceID)
}

// Weekly first, then shorter windows, matching the OpenAI account rows.
func claudeWindowOrder(windows []storage.ClaudeWindow) []int {
	order := make([]int, len(windows))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(i, j int) bool { return windows[order[i]].WindowSeconds > windows[order[j]].WindowSeconds })
	return order
}
