package catalog

import (
	"encoding/json"
	"fmt"
	"slices"
)

// A device sees one picker entry per model, but access belongs to accounts.
// Advertise the programs available through any eligible account without ever
// changing an account snapshot. Routing checks those original snapshots.
func nativePickerDefinition(definitions []nativeDefinition, chosen int) (json.RawMessage, error) {
	programs := map[string][]string{}
	for _, definition := range definitions {
		var model struct {
			Available map[string][]string `json:"available_access_programs"`
		}
		if err := json.Unmarshal(definition.raw, &model); err != nil {
			return nil, fmt.Errorf("decode native model access programs: %w", err)
		}
		for category, available := range model.Available {
			if _, exists := programs[category]; !exists {
				programs[category] = []string{}
			}
			for _, program := range available {
				if !slices.Contains(programs[category], program) {
					programs[category] = append(programs[category], program)
				}
			}
		}
	}
	if len(programs) == 0 {
		return definitions[chosen].raw, nil
	}
	// Keep access-program ordering independent of which account is primary. Standard is
	// the ordinary explicit choice; other program names have stable ordering.
	for _, available := range programs {
		slices.SortFunc(available, func(a, b string) int {
			if a == b {
				return 0
			}
			if a == "standard" {
				return -1
			}
			if b == "standard" {
				return 1
			}
			if a < b {
				return -1
			}
			return 1
		})
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(definitions[chosen].raw, &entry); err != nil {
		return nil, err
	}
	entry["available_access_programs"], _ = json.Marshal(programs)
	return json.Marshal(entry)
}

// Account entitlements are expected to differ. They are not model-definition
// conflicts and must not participate in the primary-definition comparison.
func modelDefinitionWithoutAccess(value any) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	definition := make(map[string]any, len(object))
	for key, value := range object {
		if key != "available_access_programs" {
			definition[key] = value
		}
	}
	return definition
}
