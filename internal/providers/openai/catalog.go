package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencdx/opencdx/internal/providers"
)

func ParseNativeModels(raw []byte) ([]providers.DiscoveredModel, error) {
	var payload struct {
		Models []json.RawMessage `json:"models"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode native model catalog: %w", err)
	}
	if len(payload.Models) == 0 {
		return nil, errors.New("native model catalog was empty")
	}
	models := make([]providers.DiscoveredModel, 0, len(payload.Models))
	for _, rawModel := range payload.Models {
		var identity struct {
			Slug string `json:"slug"`
		}
		if err := json.Unmarshal(rawModel, &identity); err != nil || identity.Slug == "" {
			return nil, errors.New("native model catalog contained an entry without a slug")
		}
		models = append(models, providers.DiscoveredModel{ID: identity.Slug, Raw: append(json.RawMessage(nil), rawModel...)})
	}
	return models, nil
}
