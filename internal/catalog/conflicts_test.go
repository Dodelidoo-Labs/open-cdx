package catalog

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func conflictAccount(id, plan string, primary bool, models ...string) storage.Account {
	entries := make([]json.RawMessage, len(models))
	slugs := make([]string, len(models))
	for i, model := range models {
		entries[i] = json.RawMessage(model)
		var identity struct {
			Slug string `json:"slug"`
		}
		_ = json.Unmarshal(entries[i], &identity)
		slugs[i] = identity.Slug
	}
	return storage.Account{ID: id, MaskedEmail: id + "***@example.com", Plan: plan, Primary: primary, Status: "ready", EntitledModels: slugs, RawCatalogSnapshot: catalogPayload(entries...)}
}

func TestNativeConflictsReportEveryFieldAccountAndModel(t *testing.T) {
	accounts := []storage.Account{
		conflictAccount("plus", "plus", false, `{"slug":"model-a","catalog_id":"plus-id","config":{"limit":100,"mode":"fast"},"levels":["low","high"],"shared":"DO-NOT-SHOW","nullable":null}`, `{"slug":"model-b","flag":false}`),
		conflictAccount("pro", "pro", true, `{"slug":"model-a","catalog_id":"pro-id","config":{"limit":200,"mode":"fast"},"levels":["low","high"],"shared":"DO-NOT-SHOW"}`, `{"slug":"model-b","flag":true}`),
		conflictAccount("team", "team", false, `{"slug":"model-a","catalog_id":"pro-id","config":{"limit":200,"mode":"slow"},"levels":["low","ultra"],"shared":"DO-NOT-SHOW"}`, `{"slug":"model-b","flag":false}`),
	}
	conflicts, err := NativeConflicts(accounts)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 2 || conflicts[0].Model != "model-a" || conflicts[1].Model != "model-b" {
		t.Fatalf("models missing: %#v", conflicts)
	}
	first := conflicts[0]
	var paths []string
	for _, field := range first.Fields {
		paths = append(paths, field.Path)
		if len(field.Values) != 3 {
			t.Fatal("account hidden")
		}
	}
	want := []string{"/catalog_id", "/config/limit", "/config/mode", "/levels/1", "/nullable"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("fields: %v want %v", paths, want)
	}
	if first.Sources[0].Retained || !first.Sources[1].Retained || first.Sources[2].Retained || first.Sources[1].Plan != "pro" {
		t.Fatalf("retained source: %#v", first.Sources)
	}
	id := first.Fields[0]
	if id.Values[0].JSON != `"plus-id"` || id.Values[1].JSON != `"pro-id"` || !id.Values[2].MatchesRetained || id.Values[0].MatchesRetained {
		t.Fatalf("source values: %#v", id)
	}
	nullable := first.Fields[4]
	if !nullable.Values[0].Present || nullable.Values[0].JSON != "null" || nullable.Values[1].Present {
		t.Fatal("missing and null conflated")
	}
	raw, _ := json.Marshal(conflicts)
	if strings.Contains(string(raw), "DO-NOT-SHOW") || strings.Contains(string(raw), "shared") {
		t.Fatal("unchanged catalog fields exposed")
	}
	entries, summaries, err := mergeNativeAccounts(accounts)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 || !strings.Contains(summaries["model-a"], "5 differing fields") {
		t.Fatalf("summary dropped conflicts: %v", summaries)
	}
	var entry map[string]any
	_ = json.Unmarshal(entries[0], &entry)
	if entry["catalog_id"] != "pro-id" || entry["config"].(map[string]any)["mode"] != "fast" {
		t.Fatal("retained definition was merged or replaced")
	}
}

func TestNativeConflictsSemanticEqualityAndExactNumbers(t *testing.T) {
	for _, pair := range [][2]string{{"1", "1.0"}, {"1000", "1e3"}, {"-0", "0.00"}, {"0.0010", "1e-3"}, {"1e1000000000", "10e999999999"}} {
		a, _ := decodeDefinition([]byte(pair[0]))
		b, _ := decodeDefinition([]byte(pair[1]))
		if !definitionEqual(a, b) {
			t.Fatalf("equivalent numbers differ: %v", pair)
		}
	}
	accounts := []storage.Account{
		conflictAccount("a", "pro", true, `{"slug":"same","nested":{"a":1,"b":false},"items":[null,"x"],"id":9007199254740992}`),
		conflictAccount("b", "plus", false, `{ "id":9007199254740992,"items":[null,"x"],"nested":{"b":false,"a":1.0},"slug":"same" }`),
	}
	conflicts, err := NativeConflicts(accounts)
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("formatting generated conflicts: %#v %v", conflicts, err)
	}
	accounts[1].RawCatalogSnapshot = json.RawMessage(strings.Replace(string(accounts[1].RawCatalogSnapshot), "9007199254740992", "9007199254740993", 1))
	conflicts, err = NativeConflicts(accounts)
	if err != nil || len(conflicts) != 1 || len(conflicts[0].Fields) != 1 || conflicts[0].Fields[0].Values[1].JSON != "9007199254740993" {
		t.Fatalf("large ID discrepancy rounded away: %#v %v", conflicts, err)
	}
}

func TestNativeConflictsPreserveMissingContainersAndAllArrayPositions(t *testing.T) {
	accounts := []storage.Account{
		conflictAccount("a", "pro", true, `{"slug":"model"}`),
		conflictAccount("b", "plus", false, `{"slug":"model","obj":{},"arr":[],"nil":null,"zero":0,"false":false,"empty":""}`),
		conflictAccount("c", "plus", false, `{"slug":"model","obj":{"x":1},"arr":["a","b"],"a/b~c":true}`),
	}
	conflicts, err := NativeConflicts(accounts)
	if err != nil || len(conflicts) != 1 {
		t.Fatal(err)
	}
	fields := map[string]ConflictField{}
	for _, field := range conflicts[0].Fields {
		fields[field.Path] = field
	}
	for _, path := range []string{"/obj", "/obj/x", "/arr", "/arr/0", "/arr/1", "/nil", "/zero", "/false", "/empty", "/a~1b~0c"} {
		if _, ok := fields[path]; !ok {
			t.Fatalf("discrepancy hidden at %s: %#v", path, fields)
		}
	}
	if !fields["/obj"].Container || fields["/obj"].Values[0].Present || !fields["/obj"].Values[1].Present {
		t.Fatal("missing and empty parent conflated")
	}
}

func TestNativeConflictsEligibilityFallbackAndDuplicateEntries(t *testing.T) {
	a := conflictAccount("first", "plus", false, `{"slug":"model","v":1}`)
	b := conflictAccount("second", "pro", false, `{"slug":"model","v":2}`)
	paused := conflictAccount("paused", "pro", true, `{"slug":"model","v":3}`)
	paused.Paused = true
	notReady := paused
	notReady.Paused = false
	notReady.Status = "reauthentication_required"
	unentitled := b
	unentitled.EntitledModels = nil
	conflicts, err := NativeConflicts([]storage.Account{a, b, paused, notReady, unentitled})
	if err != nil || len(conflicts) != 1 || len(conflicts[0].Sources) != 2 || !conflicts[0].Sources[0].Retained {
		t.Fatalf("wrong eligibility or fallback: %#v %v", conflicts, err)
	}
	duplicate := conflictAccount("one", "pro", true, `{"slug":"model","v":1}`, `{"slug":"model","v":2}`, `{"slug":"model","v":3}`)
	conflicts, err = NativeConflicts([]storage.Account{duplicate})
	if err != nil || len(conflicts) != 1 || len(conflicts[0].Sources) != 3 || conflicts[0].Sources[2].CatalogEntry != 3 {
		t.Fatalf("duplicate model entries hidden: %#v %v", conflicts, err)
	}
}
