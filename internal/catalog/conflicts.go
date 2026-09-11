package catalog

import (
	"bytes"
	"encoding/json"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

// NativeConflict contains only differing fields, never entire model catalogs.
// Sources and each field's Values use the same order, including accounts whose
// definition matches the retained source but differs from another account.
type NativeConflict struct {
	Model   string           `json:"model"`
	Sources []ConflictSource `json:"sources"`
	Fields  []ConflictField  `json:"fields"`
}
type ConflictSource struct {
	AccountID    string `json:"account_id"`
	Account      string `json:"account"`
	Plan         string `json:"plan"`
	Primary      bool   `json:"primary"`
	Retained     bool   `json:"retained"`
	CatalogEntry int    `json:"catalog_entry"`
}
type ConflictField struct {
	Path      string          `json:"path"`
	Container bool            `json:"container,omitempty"`
	Values    []ConflictValue `json:"values"`
}
type ConflictValue struct {
	Present bool `json:"present"`
	// JSON is text so browser number parsing cannot round large catalog IDs.
	JSON            string `json:"json,omitempty"`
	MatchesRetained bool   `json:"matches_retained"`
}
type nativeDefinition struct {
	raw    json.RawMessage
	value  any
	source ConflictSource
}
type fieldValue struct {
	value   any
	present bool
}

// NativeConflicts evaluates the same eligible definitions and selection policy
// as device catalog generation. It works with existing stored snapshots and
// does not depend on the legacy, generic catalog_conflicts cache.
func NativeConflicts(accounts []storage.Account) ([]NativeConflict, error) {
	_, conflicts, err := mergeNativeAccountsDetailed(accounts)
	if err != nil {
		return nil, err
	}
	return conflicts, nil
}

func decodeDefinition(raw []byte) (any, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err := decoder.Decode(&value)
	return value, err
}

func definitionConflict(model string, definitions []nativeDefinition, retained int) NativeConflict {
	conflict := NativeConflict{Model: model, Sources: make([]ConflictSource, len(definitions)), Fields: []ConflictField{}}
	values := make([]fieldValue, len(definitions))
	for i, definition := range definitions {
		conflict.Sources[i] = definition.source
		conflict.Sources[i].Retained = i == retained
		values[i] = fieldValue{value: definition.value, present: true}
	}
	collectDifferences("", values, retained, &conflict.Fields)
	return conflict
}

// Walk every key and array index across every source. Missing values stay
// distinct from null, false, zero, empty strings, and empty containers.
func collectDifferences(path string, values []fieldValue, retained int, fields *[]ConflictField) {
	equal := true
	for _, value := range values[1:] {
		if !fieldEqual(values[0], value) {
			equal = false
			break
		}
	}
	if equal {
		return
	}
	objects, arrays := true, true
	keys := map[string]bool{}
	longest := 0
	for _, value := range values {
		if !value.present {
			continue
		}
		object, ok := value.value.(map[string]any)
		if !ok {
			objects = false
		} else {
			for key := range object {
				keys[key] = true
			}
		}
		array, ok := value.value.([]any)
		if !ok {
			arrays = false
		} else if len(array) > longest {
			longest = len(array)
		}
	}
	if (objects && len(keys) > 0) || (arrays && longest > 0) {
		// A parent missing in one source and empty in another cannot be
		// represented solely by its children. Preserve that distinction too.
		missing := false
		for _, value := range values {
			if !value.present {
				missing = true
				break
			}
		}
		if missing {
			presence := make([]fieldValue, len(values))
			kind := "Object"
			if arrays {
				kind = "Array"
			}
			for i, value := range values {
				presence[i] = fieldValue{value: kind, present: value.present}
			}
			appendDifference(path, presence, retained, true, fields)
		}
	}
	if objects && len(keys) > 0 {
		sorted := make([]string, 0, len(keys))
		for key := range keys {
			sorted = append(sorted, key)
		}
		sort.Strings(sorted)
		for _, key := range sorted {
			children := make([]fieldValue, len(values))
			for i, value := range values {
				if object, ok := value.value.(map[string]any); ok {
					child, present := object[key]
					children[i] = fieldValue{child, present}
				}
			}
			// JSON Pointer escapes keep unusual provider field names unambiguous.
			escaped := strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
			collectDifferences(path+"/"+escaped, children, retained, fields)
		}
		return
	}
	if arrays && longest > 0 {
		for index := 0; index < longest; index++ {
			children := make([]fieldValue, len(values))
			for i, value := range values {
				if array, ok := value.value.([]any); ok && index < len(array) {
					children[i] = fieldValue{array[index], true}
				}
			}
			collectDifferences(path+"/"+strconv.Itoa(index), children, retained, fields)
		}
		return
	}
	appendDifference(path, values, retained, false, fields)
}

func appendDifference(path string, values []fieldValue, retained int, container bool, fields *[]ConflictField) {
	field := ConflictField{Path: path, Container: container, Values: make([]ConflictValue, len(values))}
	for i, value := range values {
		field.Values[i] = ConflictValue{Present: value.present, MatchesRetained: fieldEqual(values[retained], value)}
		if value.present {
			raw, _ := json.MarshalIndent(value.value, "", "  ")
			field.Values[i].JSON = string(raw)
		}
	}
	*fields = append(*fields, field)
}

func fieldEqual(left, right fieldValue) bool {
	return left.present == right.present && (!left.present || definitionEqual(left.value, right.value))
}
func definitionEqual(left, right any) bool {
	switch l := left.(type) {
	case nil:
		return right == nil
	case bool:
		r, ok := right.(bool)
		return ok && l == r
	case string:
		r, ok := right.(string)
		return ok && l == r
	case json.Number:
		r, ok := right.(json.Number)
		if !ok {
			return false
		}
		if l == r {
			return true
		}
		// Compare numerically without float64 rounding or treating 1 and 1.0 as a conflict.
		return normalizedNumber(l) == normalizedNumber(r)
	case map[string]any:
		r, ok := right.(map[string]any)
		if !ok || len(l) != len(r) {
			return false
		}
		for key, value := range l {
			other, present := r[key]
			if !present || !definitionEqual(value, other) {
				return false
			}
		}
		return true
	case []any:
		r, ok := right.([]any)
		if !ok || len(l) != len(r) {
			return false
		}
		for i, value := range l {
			if !definitionEqual(value, r[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// Normalize decimal digits and the exponent without expanding powers of ten.
// This preserves large IDs and handles huge exponents with bounded memory.
func normalizedNumber(value json.Number) string {
	text := strings.ToLower(string(value))
	parts := strings.SplitN(text, "e", 2)
	mantissa := parts[0]
	exponent := new(big.Int)
	if len(parts) == 2 {
		exponent.SetString(parts[1], 10)
	}
	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign = "-"
		mantissa = mantissa[1:]
	}
	if dot := strings.IndexByte(mantissa, '.'); dot >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(mantissa)-dot-1)))
		mantissa = mantissa[:dot] + mantissa[dot+1:]
	}
	mantissa = strings.TrimLeft(mantissa, "0")
	if mantissa == "" {
		return "0"
	}
	digits := strings.TrimRight(mantissa, "0")
	exponent.Add(exponent, big.NewInt(int64(len(mantissa)-len(digits))))
	return sign + digits + "e" + exponent.String()
}
