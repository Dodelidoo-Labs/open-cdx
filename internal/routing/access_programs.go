package routing

import (
	"encoding/json"
	"errors"

	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

// Access selections belong to a request. They must be checked against each
// candidate account's own snapshot, never the shared device catalog.
type accessPrograms map[string]string

func parseAccessPrograms(raw json.RawMessage) (accessPrograms, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var programs accessPrograms
	if err := json.Unmarshal(raw, &programs); err != nil {
		return nil, errors.New("access_programs must be an object of non-empty program names")
	}
	for category, program := range programs {
		if category == "" || program == "" {
			return nil, errors.New("access_programs must be an object of non-empty program names")
		}
	}
	return programs, nil
}

func accountSupportsAccessPrograms(account storage.Account, modelID string, requested accessPrograms) bool {
	if len(requested) == 0 {
		return true
	}
	var snapshot struct {
		Models []struct {
			Slug      string              `json:"slug"`
			Available map[string][]string `json:"available_access_programs"`
		} `json:"models"`
	}
	if json.Unmarshal(account.RawCatalogSnapshot, &snapshot) != nil {
		return false
	}
	for _, model := range snapshot.Models {
		if model.Slug != modelID {
			continue
		}
		for category, program := range requested {
			if !contains(model.Available[category], program) {
				return false
			}
		}
		return true
	}
	return false
}
