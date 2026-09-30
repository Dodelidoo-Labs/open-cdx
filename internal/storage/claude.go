package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
)

// ProviderClaudeCode identifies usage observed from Claude Code. OpenCDX never
// routes or authenticates these requests; paired helpers report them after
// Claude Code has talked to Anthropic directly. Codex reconciliation treats the
// value as reserved so the two ingestion paths never replace each other's rows.
const ProviderClaudeCode = "claude-code"

// allowanceRepeatInterval bounds identical readings from the status line,
// which can refresh several times per second while Claude Code streams.
const allowanceRepeatInterval = 5 * time.Minute

type ClaudeRequest struct {
	RequestID             string
	RecordedAt            time.Time
	Model                 string
	AccountID             string
	InputTokens           int64
	CachedInputTokens     int64
	CacheWriteInputTokens int64
	OutputTokens          int64
	ReasoningOutputTokens int64
}

type ClaudeWindow struct {
	WindowSeconds int64     `json:"window_seconds"`
	UsedPercent   float64   `json:"used_percent"`
	ResetAt       time.Time `json:"reset_at"`
}

// ClaudeAccount is an observed Claude subscription. It stores no credential:
// the identity is a helper-computed digest and the email is masked on the Mac.
type ClaudeAccount struct {
	ID             string
	IdentityHash   string
	MaskedEmail    string
	LastDeviceID   string
	LastDeviceName string
	Windows        []ClaudeWindow
	ObservedAt     time.Time
	UpdatedAt      time.Time
}

func (store *Store) EnsureClaudeAccount(ctx context.Context, identityHash, maskedEmail, deviceID string) (string, error) {
	identityHash = strings.TrimSpace(identityHash)
	if identityHash == "" || len(identityHash) > 128 || len(maskedEmail) > 320 {
		return "", errors.New("invalid Claude account identity")
	}
	id, err := secure.RandomURLSafe(18)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Unix()
	if _, err = store.db.ExecContext(ctx, `
		INSERT INTO claude_accounts(id, identity_hash, masked_email, last_device_id, created_at, updated_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(identity_hash) DO UPDATE SET
			masked_email=CASE WHEN excluded.masked_email<>'' THEN excluded.masked_email ELSE claude_accounts.masked_email END,
			last_device_id=excluded.last_device_id, updated_at=excluded.updated_at`,
		id, identityHash, maskedEmail, deviceID, now, now); err != nil {
		return "", err
	}
	err = store.db.QueryRowContext(ctx, `SELECT id FROM claude_accounts WHERE identity_hash=?`, identityHash).Scan(&id)
	return id, err
}

// RecordClaudeRequests adds each Anthropic request once, whether it first
// arrives from live telemetry or from a transcript import. It returns the
// number of requests that were new.
func (store *Store) RecordClaudeRequests(ctx context.Context, deviceID, source string, requests []ClaudeRequest) (int, error) {
	if source != UsageSourceRouted && source != UsageSourceReconciled {
		return 0, errors.New("invalid Claude usage source")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer transaction.Rollback()
	keyStatement, err := transaction.PrepareContext(ctx, `INSERT OR IGNORE INTO claude_request_keys(request_hash, recorded_at) VALUES(?,?)`)
	if err != nil {
		return 0, err
	}
	defer keyStatement.Close()
	usageStatement, err := transaction.PrepareContext(ctx, `
		INSERT INTO usage_aggregate(recorded_at, device_id, day, provider, model_id, account_id, source, routing, requests, input_tokens,
		cached_input_tokens, cache_write_input_tokens, output_tokens, reasoning_output_tokens)
		VALUES(?,?,?,?,?,?,?,?,1,?,?,?,?,?)
		ON CONFLICT(day,provider,model_id,account_id,routing,device_id,recorded_at) DO UPDATE SET requests=requests+1,
		input_tokens=input_tokens+excluded.input_tokens, cached_input_tokens=cached_input_tokens+excluded.cached_input_tokens,
		cache_write_input_tokens=cache_write_input_tokens+excluded.cache_write_input_tokens,
		output_tokens=output_tokens+excluded.output_tokens, reasoning_output_tokens=reasoning_output_tokens+excluded.reasoning_output_tokens`)
	if err != nil {
		return 0, err
	}
	defer usageStatement.Close()
	inserted := 0
	for _, request := range requests {
		recorded := request.RecordedAt.UTC()
		result, err := keyStatement.ExecContext(ctx, secure.Digest("claude-request:"+request.RequestID), recorded.Format(time.RFC3339Nano))
		if err != nil {
			return 0, err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			continue
		}
		if _, err = usageStatement.ExecContext(ctx, recorded.Format(time.RFC3339Nano), deviceID, recorded.Format("2006-01-02"), ProviderClaudeCode,
			request.Model, request.AccountID, source, UsageRoutingNative, request.InputTokens, request.CachedInputTokens,
			request.CacheWriteInputTokens, request.OutputTokens, request.ReasoningOutputTokens); err != nil {
			return 0, err
		}
		inserted++
	}
	if err = transaction.Commit(); err != nil {
		return 0, err
	}
	if inserted > 0 {
		store.telemetryRevision.Add(1)
	}
	return inserted, nil
}

// RecordClaudeAllowance updates the account's current windows and appends
// account-attributed readings for the telemetry overlay. Unchanged readings
// are stored at most every five minutes.
func (store *Store) RecordClaudeAllowance(ctx context.Context, accountID string, observedAt time.Time, windows []ClaudeWindow) error {
	if accountID == "" || len(windows) == 0 {
		return errors.New("invalid Claude allowance")
	}
	observedAt = observedAt.UTC()
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var storedObserved int64
	if err = transaction.QueryRowContext(ctx, `SELECT observed_at FROM claude_accounts WHERE id=?`, accountID).Scan(&storedObserved); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	changed := false
	for _, window := range windows {
		observation := AllowanceObservation{Source: "live", AccountID: accountID, ObservedAt: observedAt, ResetAt: window.ResetAt.UTC(), Used: window.UsedPercent, WindowSeconds: window.WindowSeconds}
		if err = observation.Validate(time.Now()); err != nil {
			return err
		}
		var lastObserved, lastReset string
		var lastUsed float64
		err = transaction.QueryRowContext(ctx, `SELECT observed_at, reset_at, used_percent FROM allowance_observations
			WHERE source='live' AND account_id=? AND device_id='' AND window_seconds=? ORDER BY observed_at DESC LIMIT 1`,
			accountID, window.WindowSeconds).Scan(&lastObserved, &lastReset, &lastUsed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			previousObserved, observedErr := time.Parse(time.RFC3339Nano, lastObserved)
			previousReset, resetErr := time.Parse(time.RFC3339Nano, lastReset)
			if observedErr == nil && resetErr == nil && lastUsed == window.UsedPercent &&
				previousReset.Sub(observation.ResetAt).Abs() <= time.Minute &&
				observedAt.Sub(previousObserved) < allowanceRepeatInterval && !observedAt.Before(previousObserved) {
				continue
			}
		}
		if err = insertAllowanceObservation(ctx, transaction, observation); err != nil {
			return err
		}
		changed = true
	}
	if observedAt.Unix() >= storedObserved {
		encoded, err := json.Marshal(windows)
		if err != nil {
			return err
		}
		if _, err = transaction.ExecContext(ctx, `UPDATE claude_accounts SET windows_json=?, observed_at=?, updated_at=? WHERE id=?`,
			encoded, observedAt.Unix(), time.Now().UTC().Unix(), accountID); err != nil {
			return err
		}
		changed = true
	}
	if err = transaction.Commit(); err != nil {
		return err
	}
	if changed {
		store.telemetryRevision.Add(1)
	}
	return nil
}

func (store *Store) ClaudeAccounts(ctx context.Context) ([]ClaudeAccount, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT c.id, c.identity_hash, c.masked_email, c.last_device_id, COALESCE(d.name,''), c.windows_json, c.observed_at, c.updated_at
		FROM claude_accounts c LEFT JOIN devices d ON d.id=c.last_device_id ORDER BY c.created_at, c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ClaudeAccount, 0)
	for rows.Next() {
		var account ClaudeAccount
		var windows []byte
		var observed, updated int64
		if err = rows.Scan(&account.ID, &account.IdentityHash, &account.MaskedEmail, &account.LastDeviceID, &account.LastDeviceName, &windows, &observed, &updated); err != nil {
			return nil, err
		}
		if len(windows) > 0 {
			if err = json.Unmarshal(windows, &account.Windows); err != nil {
				return nil, err
			}
		}
		account.ObservedAt, account.UpdatedAt = fromUnix(observed), fromUnix(updated)
		result = append(result, account)
	}
	return result, rows.Err()
}

// DeleteClaudeAccountIdentity removes an observed subscription and its
// allowance readings. Its usage rows keep their opaque account key.
func (store *Store) DeleteClaudeAccountIdentity(ctx context.Context, identityHash string) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var id string
	if err = transaction.QueryRowContext(ctx, `SELECT id FROM claude_accounts WHERE identity_hash=?`, identityHash).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM allowance_observations WHERE source='live' AND account_id=?`, id); err != nil {
		return err
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM claude_accounts WHERE id=?`, id); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return err
	}
	store.telemetryRevision.Add(1)
	return nil
}
