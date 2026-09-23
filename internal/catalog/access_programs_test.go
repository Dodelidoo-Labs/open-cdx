package catalog

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func TestPickerIncludesSecondaryAccessWithoutConflictsOrAccountMutation(t *testing.T) {
	for _, primaryAccess := range []string{
		``, `,"available_access_programs":null`,
		`,"available_access_programs":{"cyber":[]}`,
		`,"available_access_programs":{"cyber":["standard"]}`,
	} {
		for _, primaryIndex := range []int{0, 1} {
			accounts := []storage.Account{
				conflictAccount("standard", "plus", primaryIndex == 0,
					`{"slug":"shared","opaque_id":9007199254740993`+primaryAccess+`}`,
					`{"slug":"codex-auto-review"`+primaryAccess+`}`),
				conflictAccount("blue", "pro", primaryIndex == 1,
					`{"slug":"shared","opaque_id":9007199254740993,"available_access_programs":{"cyber":["standard","daybreak_blue"],"future":["special"]}}`,
					`{"slug":"codex-auto-review","available_access_programs":{"cyber":["standard","daybreak_blue"]}}`,
					`{"slug":"secondary-only","available_access_programs":{"cyber":["daybreak_blue"]}}`),
			}
			before := [][]byte{bytes.Clone(accounts[0].RawCatalogSnapshot), bytes.Clone(accounts[1].RawCatalogSnapshot)}
			entries, conflicts, err := mergeNativeAccounts(accounts)
			if err != nil || len(conflicts) != 0 || len(entries) != 3 {
				t.Fatalf("primary=%d access=%s: entries=%s conflicts=%v error=%v", primaryIndex, primaryAccess, entries, conflicts, err)
			}
			for _, entry := range entries {
				var model struct {
					Slug      string              `json:"slug"`
					Available map[string][]string `json:"available_access_programs"`
					ID        json.Number         `json:"opaque_id"`
				}
				if err := json.Unmarshal(entry, &model); err != nil {
					t.Fatal(err)
				}
				want := []string{"standard", "daybreak_blue"}
				if model.Slug == "secondary-only" {
					want = []string{"daybreak_blue"}
				}
				if !reflect.DeepEqual(model.Available["cyber"], want) {
					t.Fatalf("primary=%d access=%s lost secondary access: %s", primaryIndex, primaryAccess, entry)
				}
				if model.Slug == "shared" && (model.ID != "9007199254740993" || !reflect.DeepEqual(model.Available["future"], []string{"special"})) {
					t.Fatalf("unknown fields or access categories lost: %s", entry)
				}
			}
			for i, account := range accounts {
				if !bytes.Equal(account.RawCatalogSnapshot, before[i]) {
					t.Fatal("picker union mutated an account snapshot")
				}
			}
		}
	}
}

func TestAccessUnionExcludesIneligibleAccounts(t *testing.T) {
	standard := conflictAccount("standard", "plus", true, `{"slug":"model","available_access_programs":{"cyber":["standard"]}}`)
	for _, reason := range []string{"paused", "not-ready", "not-entitled"} {
		blue := conflictAccount("blue", "pro", false, `{"slug":"model","available_access_programs":{"cyber":["daybreak_blue"]}}`)
		switch reason {
		case "paused":
			blue.Paused = true
		case "not-ready":
			blue.Status = "reauthentication_required"
		case "not-entitled":
			blue.EntitledModels = nil
		}
		entries, conflicts, err := mergeNativeAccounts([]storage.Account{standard, blue})
		if err != nil || len(entries) != 1 || len(conflicts) != 0 || bytes.Contains(entries[0], []byte("daybreak_blue")) {
			t.Fatalf("%s account contributed access: %s, %v, %v", reason, entries, conflicts, err)
		}
	}
}

func TestOnlyModelDefinitionDifferencesRemainConflicts(t *testing.T) {
	accounts := []storage.Account{
		conflictAccount("standard", "plus", true, `{"slug":"model","context_window":100,"available_access_programs":{"cyber":["standard"]}}`),
		conflictAccount("blue", "pro", false, `{"slug":"model","context_window":200,"available_access_programs":{"cyber":["standard","daybreak_blue"]}}`),
	}
	conflicts, err := NativeConflicts(accounts)
	if err != nil || len(conflicts) != 1 || len(conflicts[0].Fields) != 1 || conflicts[0].Fields[0].Path != "/context_window" {
		t.Fatalf("access differences treated as model conflicts: %#v, %v", conflicts, err)
	}
}
