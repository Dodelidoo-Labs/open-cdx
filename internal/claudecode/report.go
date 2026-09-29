// Package claudecode turns data that Claude Code already exposes to local
// hooks into aggregate OpenCDX telemetry. It reads the status line's plan
// allowance, OpenTelemetry api_request events, and transcript usage records.
// It never reads prompts or responses and never handles Claude credentials.
package claudecode

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const ReportVersion = 1

const (
	SourceLive    = "live"
	SourceHistory = "history"
)

// Report is the helper-to-router upload. Token fields keep Anthropic's
// meaning: input tokens exclude cache reads and cache writes.
type Report struct {
	Version      int         `json:"version"`
	Source       string      `json:"source"`
	Accounts     []Account   `json:"accounts,omitempty"`
	Requests     []Request   `json:"requests,omitempty"`
	Allowances   []Allowance `json:"allowances,omitempty"`
	FilesScanned int         `json:"files_scanned,omitempty"`
}

// Account identifies a subscription by an irreversible digest of Claude
// Code's account UUID. The email is masked before it leaves the Mac.
type Account struct {
	Identity    string `json:"identity"`
	MaskedEmail string `json:"masked_email,omitempty"`
}

type Request struct {
	ID                  string `json:"request_id"`
	At                  string `json:"at"`
	Model               string `json:"model"`
	Account             string `json:"account,omitempty"`
	InputTokens         int64  `json:"input_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	ThinkingTokens      int64  `json:"thinking_tokens,omitempty"`
}

type Allowance struct {
	Account    string   `json:"account,omitempty"`
	ObservedAt string   `json:"observed_at"`
	Windows    []Window `json:"windows"`
}

type Window struct {
	Seconds     int64   `json:"window_seconds"`
	UsedPercent float64 `json:"used_percent"`
	ResetAt     string  `json:"reset_at"`
}

type Result struct {
	RequestsAdded      int `json:"requests_added"`
	RequestsDuplicated int `json:"requests_already_recorded"`
	AllowancesRecorded int `json:"allowances_recorded"`
}

// AccountIdentity digests Claude Code's account UUID with a fixed domain
// separator so the router can group readings without learning the UUID.
func AccountIdentity(accountUUID string) string {
	accountUUID = strings.TrimSpace(accountUUID)
	if accountUUID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte("opencdx-claude-account:" + accountUUID))
	return hex.EncodeToString(digest[:])
}

// ValidIdentity accepts only AccountIdentity output.
func ValidIdentity(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
