package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
)

// models projects the same atomically refreshed catalog Codex consumes. Read it
// on every request: caching only the IDs would miss capability-only changes.
func (daemon *Daemon) models(writer http.ResponseWriter, request *http.Request) {
	file, err := os.Open(daemon.catalogPath)
	if err != nil {
		writeHelperJSON(writer, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"type": "catalog_unavailable", "message": "model catalog is not available yet"}})
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
	if err == nil && len(raw) <= 32<<20 {
		raw, err = compatibleModels(raw)
	} else {
		err = errors.New("catalog read failed")
	}
	if err != nil {
		writeHelperJSON(writer, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"type": "catalog_unavailable", "message": "model catalog could not be read"}})
		return
	}
	hash := sha256.Sum256(raw)
	etag := `"` + hex.EncodeToString(hash[:]) + `"`
	writer.Header().Set("ETag", etag)
	writer.Header().Set("Cache-Control", "private, no-cache")
	writer.Header().Set("Vary", "Authorization")
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if request.Header.Get("If-None-Match") == etag {
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = writer.Write(raw)
}

func compatibleModels(raw []byte) ([]byte, error) {
	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, err
	}
	if catalog.Models == nil {
		return nil, errors.New("missing model list")
	}
	data := make([]map[string]any, 0, len(catalog.Models))
	for _, source := range catalog.Models {
		var slug, visibility string
		if err := json.Unmarshal(source["slug"], &slug); err != nil || strings.TrimSpace(slug) == "" {
			return nil, errors.New("invalid model ID")
		}
		_ = json.Unmarshal(source["visibility"], &visibility)
		if visibility != "list" {
			continue
		}
		owner := "openai"
		if prefix, _, ok := strings.Cut(slug, "/"); ok {
			owner = prefix
		}
		model := map[string]any{"id": slug, "object": "model", "owned_by": owner}
		// Whitelist discovery metadata. Catalog entries also contain instructions
		// and client execution settings, which are not part of a models API.
		for target, original := range map[string]string{
			"name": "display_name", "description": "description",
			"context_length": "context_window", "max_context_length": "max_context_window",
			"input_modalities":           "input_modalities",
			"supported_reasoning_levels": "supported_reasoning_levels",
			"default_reasoning_level":    "default_reasoning_level",
			"service_tiers":              "service_tiers", "default_service_tier": "default_service_tier",
		} {
			if value, ok := source[original]; ok {
				model[target] = value
			}
		}
		var levels []struct {
			Effort string `json:"effort"`
		}
		if levelsRaw, ok := source["supported_reasoning_levels"]; ok {
			if err := json.Unmarshal(levelsRaw, &levels); err != nil {
				return nil, err
			}
			efforts := make([]string, 0, len(levels))
			for _, level := range levels {
				if level.Effort != "" {
					efforts = append(efforts, level.Effort)
				}
			}
			reasoning := map[string]any{"supported_efforts": efforts}
			if value, ok := source["default_reasoning_level"]; ok {
				reasoning["default_effort"] = value
			}
			model["reasoning"] = reasoning
		}
		data = append(data, model)
	}
	return json.Marshal(map[string]any{"object": "list", "data": data})
}
