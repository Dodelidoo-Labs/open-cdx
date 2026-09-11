package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RequestLog deliberately contains metadata only, never request/response bodies or credentials.
type RequestLog struct {
	ID              string              `json:"id"`
	StartedAt       time.Time           `json:"started_at"`
	DurationMS      int64               `json:"duration_ms"`
	DeviceID        string              `json:"device_id,omitempty"`
	DeviceName      string              `json:"device_name,omitempty"`
	Method          string              `json:"method"`
	Path            string              `json:"path"`
	Model           string              `json:"model,omitempty"`
	UpstreamModel   string              `json:"upstream_model,omitempty"`
	Provider        string              `json:"provider,omitempty"`
	ReasoningEffort string              `json:"reasoning_effort,omitempty"`
	ServiceTier     string              `json:"service_tier,omitempty"`
	Stream          bool                `json:"stream"`
	RequestBytes    int64               `json:"request_bytes"`
	ResponseBytes   int64               `json:"response_bytes"`
	ThreadID        string              `json:"thread_id,omitempty"`
	SessionID       string              `json:"session_id,omitempty"`
	ClientVersion   string              `json:"client_version,omitempty"`
	Status          int                 `json:"status"`
	Outcome         string              `json:"outcome"`
	ErrorType       string              `json:"error_type,omitempty"`
	ErrorCode       string              `json:"error_code,omitempty"`
	ErrorMessage    string              `json:"error_message,omitempty"`
	ResponseID      string              `json:"response_id,omitempty"`
	ResponseModel   string              `json:"response_model,omitempty"`
	Usage           *RequestLogUsage    `json:"usage,omitempty"`
	Attempts        []RequestLogAttempt `json:"attempts"`
}
type RequestLogUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
}
type RequestLogAttempt struct {
	AccountID    string `json:"account_id,omitempty"`
	Account      string `json:"account,omitempty"`
	Status       int    `json:"status"`
	DurationMS   int64  `json:"duration_ms"`
	HeadersMS    int64  `json:"headers_ms"`
	FirstByteMS  *int64 `json:"first_byte_ms,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	ErrorType    string `json:"error_type,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}
type RequestLogFilter struct {
	Before                             int64
	Limit                              int
	Provider, Model, DeviceID, Outcome string
}
type RequestLogPage struct {
	Logs       []RequestLog `json:"logs"`
	NextBefore int64        `json:"next_before,omitempty"`
}

func (store *Store) RecordRequestLog(ctx context.Context, entry RequestLog) error {
	// Snapshot the display name so history remains useful after device removal.
	if entry.DeviceName == "" {
		_ = store.db.QueryRowContext(ctx, "SELECT name FROM devices WHERE id=?", entry.DeviceID).Scan(&entry.DeviceName)
	}
	_, err := store.ImportRequestLogs(ctx, []RequestLog{entry})
	return err
}

func validateRequestLog(entry RequestLog) error {
	if entry.ID == "" || len(entry.ID) > 128 || entry.StartedAt.IsZero() || entry.DurationMS < 0 || entry.Status < 0 || entry.Status > 599 || len(entry.Attempts) > 8 {
		return errors.New("invalid request log metadata")
	}
	switch entry.Outcome {
	case "success", "error", "cancelled", "incomplete":
	default:
		return errors.New("invalid request log outcome")
	}
	if entry.Usage != nil && (entry.Usage.InputTokens < 0 || entry.Usage.OutputTokens < 0 || entry.Usage.CachedInputTokens < 0 || entry.Usage.ReasoningOutputTokens < 0) {
		return errors.New("invalid token counts")
	}
	raw, err := json.Marshal(entry)
	if err != nil || len(raw) > 32<<10 {
		return errors.New("request log exceeds 32 KiB")
	}
	return nil
}

// ImportRequestLogs is atomic and idempotent, with no effect on usage totals or routing.
func (store *Store) ImportRequestLogs(ctx context.Context, entries []RequestLog) (int64, error) {
	for _, entry := range entries {
		if err := validateRequestLog(entry); err != nil {
			return 0, err
		}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO request_logs(id,started_at,provider,model,device_id,outcome,metadata) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	var count int64
	for _, entry := range entries {
		entry.StartedAt = entry.StartedAt.UTC()
		raw, _ := json.Marshal(entry)
		result, err := stmt.ExecContext(ctx, entry.ID, entry.StartedAt.Format("2006-01-02T15:04:05.000000000Z"), entry.Provider, entry.Model, entry.DeviceID, entry.Outcome, raw)
		if err != nil {
			return 0, err
		}
		n, _ := result.RowsAffected()
		count += n
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func (store *Store) RequestLogs(ctx context.Context, filter RequestLogFilter) (RequestLogPage, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	query := "SELECT sequence,metadata FROM request_logs WHERE 1=1"
	args := []any{}
	if filter.Before > 0 {
		query += " AND (started_at, id) < (SELECT started_at, id FROM request_logs WHERE sequence = ?)"
		args = append(args, filter.Before)
	}
	for _, field := range []struct{ name, value string }{{"provider", filter.Provider}, {"model", filter.Model}, {"device_id", filter.DeviceID}, {"outcome", filter.Outcome}} {
		if field.value != "" {
			query += " AND " + field.name + " = ?"
			args = append(args, field.value)
		}
	}
	query += " ORDER BY started_at DESC, id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return RequestLogPage{}, err
	}
	defer rows.Close()
	page := RequestLogPage{Logs: []RequestLog{}}
	var last int64
	for rows.Next() {
		var seq int64
		var raw []byte
		if err = rows.Scan(&seq, &raw); err != nil {
			return page, err
		}
		if len(page.Logs) == limit {
			page.NextBefore = last
			break
		}
		var entry RequestLog
		if err = json.Unmarshal(raw, &entry); err != nil {
			return page, fmt.Errorf("decode request log: %w", err)
		}
		page.Logs = append(page.Logs, entry)
		last = seq
	}
	return page, rows.Err()
}

func (store *Store) RequestLog(ctx context.Context, id string) (RequestLog, error) {
	var raw []byte
	var entry RequestLog
	err := store.db.QueryRowContext(ctx, "SELECT metadata FROM request_logs WHERE id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, ErrNotFound
	}
	if err != nil {
		return entry, err
	}
	err = json.Unmarshal(raw, &entry)
	return entry, err
}
