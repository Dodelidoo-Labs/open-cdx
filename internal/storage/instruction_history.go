package storage

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
)

type InstructionChange struct {
	Path       string `json:"path"`
	BeforeHash string `json:"before_hash,omitempty"`
	AfterHash  string `json:"after_hash,omitempty"`
}
type InstructionRevision struct {
	ID                    int64               `json:"id"`
	ObservedAt            string              `json:"observed_at"`
	PreviousObservedAt    string              `json:"previous_observed_at,omitempty"`
	AccountID             string              `json:"account_id"`
	Account               string              `json:"account"`
	Plan                  string              `json:"plan"`
	Model                 string              `json:"model"`
	ClientVersion         string              `json:"client_version,omitempty"`
	PreviousClientVersion string              `json:"previous_client_version,omitempty"`
	Kind                  string              `json:"kind"`
	Changes               []InstructionChange `json:"changes"`
}
type InstructionHistoryFilter struct {
	Before                 int64
	Model, AccountID, Kind string
}
type InstructionHistoryPage struct {
	Revisions  []InstructionRevision `json:"revisions"`
	NextBefore int64                 `json:"next_before,omitempty"`
}
type InstructionHistoryStatus struct {
	LatestChangeID int64 `json:"latest_change_id"`
	Unseen         int64 `json:"unseen"`
}
type InstructionFieldVersion struct {
	Present bool   `json:"present"`
	Kind    string `json:"kind,omitempty"`
	Text    string `json:"text"`
	Hash    string `json:"hash,omitempty"`
}
type instructionState map[string]map[string]string

func instructionFields(raw []byte) (map[string]map[string][]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil || root == nil {
		return nil, errors.New("native catalog must be a JSON object")
	}
	var models []map[string]json.RawMessage
	if err := json.Unmarshal(root["models"], &models); err != nil {
		return nil, errors.New("native catalog models are invalid")
	}
	result := map[string]map[string][]byte{}
	rootFields := map[string][]byte{}
	for key, value := range root {
		if key != "models" {
			if err := collectInstructionFields("/"+instructionPointer(key), key, value, false, rootFields); err != nil {
				return nil, err
			}
		}
	}
	if len(rootFields) > 0 {
		result["(catalog)"] = rootFields
	}
	occurrences := map[string]int{}
	for _, model := range models {
		var slug string
		if err := json.Unmarshal(model["slug"], &slug); err != nil || slug == "" {
			return nil, errors.New("native catalog model has no slug")
		}
		occurrences[slug]++
		name := slug
		if occurrences[slug] > 1 {
			name = fmt.Sprintf("%s (definition %d)", slug, occurrences[slug])
		}
		fields := map[string][]byte{}
		for key, value := range model {
			if err := collectInstructionFields("/"+instructionPointer(key), key, value, false, fields); err != nil {
				return nil, err
			}
		}
		result[name] = fields
	}
	return result, nil
}
func instructionPointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

// Model messages include templates, variables, approval/collaboration policies,
// and other instruction-bearing text even when a leaf isn't named instructions.
func collectInstructionFields(path, key string, raw json.RawMessage, selected bool, out map[string][]byte) error {
	selected = selected || strings.Contains(strings.ToLower(key), "instruction") || key == "model_messages"
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return errors.New("empty native catalog value")
	}
	switch trimmed[0] {
	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		if len(object) > 0 {
			for key, value := range object {
				if err := collectInstructionFields(path+"/"+instructionPointer(key), key, value, selected, out); err != nil {
					return err
				}
			}
			return nil
		}
	case '[':
		var array []json.RawMessage
		if err := json.Unmarshal(raw, &array); err != nil {
			return err
		}
		if len(array) > 0 {
			for index, value := range array {
				if err := collectInstructionFields(path+"/"+strconv.Itoa(index), "", value, selected, out); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if selected {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			return err
		}
		out[path] = canonical
	}
	return nil
}

// Catalog snapshots and history commit together, so concurrent refreshes cannot
// lose intermediate observations or record changes to a catalog that wasn't saved.
func (store *Store) putCatalogSnapshotTx(ctx context.Context, tx *sql.Tx, snapshot CatalogSnapshot) error {
	if snapshot.FetchedAt.IsZero() {
		snapshot.FetchedAt = time.Now().UTC()
	}
	if snapshot.Provider == "openai" {
		if err := store.recordInstructionSnapshot(ctx, tx, snapshot); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog_snapshots(provider,account_id,etag,raw_json,fetched_at) VALUES(?,?,?,?,?)
 ON CONFLICT(provider,account_id) DO UPDATE SET etag=excluded.etag,raw_json=excluded.raw_json,fetched_at=excluded.fetched_at`, snapshot.Provider, snapshot.AccountID, snapshot.ETag, []byte(snapshot.Raw), unixTime(snapshot.FetchedAt))
	return err
}
func (store *Store) recordInstructionSnapshot(ctx context.Context, tx *sql.Tx, snapshot CatalogSnapshot) error {
	fields, err := instructionFields(snapshot.Raw)
	if err != nil {
		return err
	}
	previous := instructionState{}
	var previousJSON []byte
	var previousVersion, previousAt string
	err = tx.QueryRowContext(ctx, `SELECT fields_json,client_version,observed_at FROM instruction_catalog_state WHERE account_id=?`, snapshot.AccountID).Scan(&previousJSON, &previousVersion, &previousAt)
	baseline := errors.Is(err, sql.ErrNoRows)
	if err != nil && !baseline {
		return err
	}
	if !baseline {
		if err = json.Unmarshal(previousJSON, &previous); err != nil {
			return err
		}
	}
	var account, plan string
	err = tx.QueryRowContext(ctx, `SELECT masked_email,plan FROM accounts WHERE id=?`, snapshot.AccountID).Scan(&account, &plan)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	current := instructionState{}
	content := map[string][]byte{}
	for model, paths := range fields {
		current[model] = map[string]string{}
		for path, raw := range paths {
			sum := sha256.Sum256(raw)
			hash := hex.EncodeToString(sum[:])
			current[model][path] = hash
			content[hash] = raw
		}
	}
	modelSet := map[string]bool{}
	for model := range previous {
		modelSet[model] = true
	}
	for model := range current {
		modelSet[model] = true
	}
	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	sort.Strings(models)
	observedAt := snapshot.FetchedAt.UTC().Format(time.RFC3339Nano)
	saved := map[string]bool{}
	for _, model := range models {
		pathSet := map[string]bool{}
		for path := range previous[model] {
			pathSet[path] = true
		}
		for path := range current[model] {
			pathSet[path] = true
		}
		paths := make([]string, 0, len(pathSet))
		for path := range pathSet {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		revision := InstructionRevision{ObservedAt: observedAt, PreviousObservedAt: previousAt, AccountID: snapshot.AccountID, Account: account, Plan: plan, Model: model, ClientVersion: snapshot.ClientVersion, PreviousClientVersion: previousVersion, Kind: "changed", Changes: []InstructionChange{}}
		if baseline {
			revision.Kind = "baseline"
		}
		for _, path := range paths {
			before, after := previous[model][path], current[model][path]
			if before == after {
				continue
			}
			if after != "" && !saved[after] {
				var exists int
				if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM instruction_contents WHERE hash=?)`, after).Scan(&exists); err != nil {
					return err
				}
				if exists == 0 {
					var compressed bytes.Buffer
					zip := gzip.NewWriter(&compressed)
					if _, err = zip.Write(content[after]); err != nil {
						return err
					}
					if err = zip.Close(); err != nil {
						return err
					}
					if _, err = tx.ExecContext(ctx, `INSERT INTO instruction_contents(hash,content_gzip) VALUES(?,?)`, after, compressed.Bytes()); err != nil {
						return err
					}
				}
				saved[after] = true
			}
			revision.Changes = append(revision.Changes, InstructionChange{Path: path, BeforeHash: before, AfterHash: after})
		}
		if len(revision.Changes) == 0 {
			continue
		}
		metadata, err := json.Marshal(revision)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO instruction_revisions(account_id,model,kind,metadata) VALUES(?,?,?,?)`, snapshot.AccountID, model, revision.Kind, metadata); err != nil {
			return err
		}
	}
	stateJSON, err := json.Marshal(current)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO instruction_catalog_state(account_id,fields_json,client_version,observed_at) VALUES(?,?,?,?)
 ON CONFLICT(account_id) DO UPDATE SET fields_json=excluded.fields_json,client_version=excluded.client_version,observed_at=excluded.observed_at`, snapshot.AccountID, stateJSON, snapshot.ClientVersion, observedAt)
	return err
}

// Backfill a baseline from existing snapshots once, without inventing earlier history.
func (store *Store) initializeInstructionHistory(ctx context.Context) error {
	snapshots, err := store.CatalogSnapshots(ctx, "openai")
	if err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		var exists int
		if err = store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM instruction_catalog_state WHERE account_id=?)`, snapshot.AccountID).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			continue
		}
		if _, err = instructionFields(snapshot.Raw); err != nil {
			slog.Warn("instruction baseline skipped for invalid cached catalog", "account_id", snapshot.AccountID)
			continue
		}
		if snapshot.FetchedAt.IsZero() {
			snapshot.FetchedAt = time.Now().UTC()
		}
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		err = store.recordInstructionSnapshot(ctx, tx, snapshot)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) InstructionHistory(ctx context.Context, filter InstructionHistoryFilter) (InstructionHistoryPage, error) {
	query := `SELECT id,metadata FROM instruction_revisions WHERE 1=1`
	args := []any{}
	if filter.Before > 0 {
		query += ` AND id<?`
		args = append(args, filter.Before)
	}
	for _, field := range []struct{ name, value string }{{"model", filter.Model}, {"account_id", filter.AccountID}, {"kind", filter.Kind}} {
		if field.value != "" {
			query += " AND " + field.name + "=?"
			args = append(args, field.value)
		}
	}
	query += ` ORDER BY id DESC LIMIT 51`
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return InstructionHistoryPage{}, err
	}
	defer rows.Close()
	page := InstructionHistoryPage{Revisions: []InstructionRevision{}}
	for rows.Next() {
		var id int64
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return page, err
		}
		if len(page.Revisions) == 50 {
			page.NextBefore = page.Revisions[49].ID
			break
		}
		var revision InstructionRevision
		if err = json.Unmarshal(raw, &revision); err != nil {
			return page, err
		}
		revision.ID = id
		page.Revisions = append(page.Revisions, revision)
	}
	return page, rows.Err()
}
func (store *Store) InstructionRevision(ctx context.Context, id int64) (InstructionRevision, error) {
	var raw []byte
	var revision InstructionRevision
	err := store.db.QueryRowContext(ctx, `SELECT metadata FROM instruction_revisions WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return revision, ErrNotFound
	}
	if err != nil {
		return revision, err
	}
	err = json.Unmarshal(raw, &revision)
	revision.ID = id
	return revision, err
}
func (store *Store) InstructionStatus(ctx context.Context, after int64) (InstructionHistoryStatus, error) {
	var status InstructionHistoryStatus
	err := store.db.QueryRowContext(ctx, `SELECT (SELECT COALESCE(MAX(id),0) FROM instruction_revisions WHERE kind='changed'),(SELECT COUNT(*) FROM instruction_revisions WHERE kind='changed' AND id>?)`, after).Scan(&status.LatestChangeID, &status.Unseen)
	return status, err
}
func (store *Store) InstructionField(ctx context.Context, hash string) (InstructionFieldVersion, error) {
	if hash == "" {
		return InstructionFieldVersion{}, nil
	}
	var compressed []byte
	if err := store.db.QueryRowContext(ctx, `SELECT content_gzip FROM instruction_contents WHERE hash=?`, hash).Scan(&compressed); err != nil {
		return InstructionFieldVersion{}, err
	}
	zip, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return InstructionFieldVersion{}, err
	}
	defer zip.Close()
	raw, err := io.ReadAll(io.LimitReader(zip, (32<<20)+1))
	if err != nil || len(raw) > 32<<20 {
		return InstructionFieldVersion{}, errors.New("instruction content is invalid or too large")
	}
	version := InstructionFieldVersion{Present: true, Hash: hash, Kind: "json"}
	var text string
	if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &text) == nil {
		version.Kind = "text"
		version.Text = text
	} else {
		var pretty bytes.Buffer
		if err = json.Indent(&pretty, raw, "", "  "); err != nil {
			return version, err
		}
		version.Text = pretty.String()
	}
	return version, nil
}
