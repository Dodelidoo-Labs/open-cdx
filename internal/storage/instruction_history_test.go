package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func historyAccount(t *testing.T, store *Store, raw string) Account {
	t.Helper()
	input := accountInput("instructions-account", "access-secret")
	input.RawCatalogSnapshot = json.RawMessage(raw)
	input.CatalogClientVersion = "1.0.0"
	account, _, err := store.PutAccount(context.Background(), input, false)
	if err != nil {
		t.Fatal(err)
	}
	return account
}
func historyPage(t *testing.T, store *Store) InstructionHistoryPage {
	t.Helper()
	page, err := store.InstructionHistory(context.Background(), InstructionHistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return page
}
func TestInstructionHistoryTracksOnlyInstructionContentAndEveryChangedField(t *testing.T) {
	store := testStore(t, ":memory:")
	ctx := context.Background()
	original := `{"models":[{"slug":"gpt-test","base_instructions":"shared baseline","description":"old metadata","model_messages":{"instructions_template":"template v1","instructions_variables":{"persona":"friendly"},"permissions":"ask before writing"},"other":{"custom_instructions":"custom"}},{"slug":"model-b","base_instructions":"shared baseline"}],"global_instructions":"global"}`
	account := historyAccount(t, store, original)
	page := historyPage(t, store)
	if len(page.Revisions) != 3 {
		t.Fatalf("missing baselines: %#v", page)
	}
	for _, revision := range page.Revisions {
		if revision.Kind != "baseline" {
			t.Fatal("first observation treated as a change")
		}
	}
	status, err := store.InstructionStatus(ctx, 0)
	if err != nil || status.Unseen != 0 {
		t.Fatalf("baseline notification: %#v %v", status, err)
	}
	// Reordered JSON and unrelated catalog updates do not create a revision.
	unchanged := strings.Replace(original, `"old metadata"`, `"new metadata"`, 1)
	if err = store.UpdateAccountCatalog(ctx, account.ID, json.RawMessage(unchanged), []string{"gpt-test", "model-b"}, "1.1.0"); err != nil {
		t.Fatal(err)
	}
	if len(historyPage(t, store).Revisions) != 3 {
		t.Fatal("ordinary catalog refresh created history")
	}
	if err = store.PutCatalogSnapshot(ctx, CatalogSnapshot{Provider: "openrouter", Raw: json.RawMessage(`{"instructions":"third party"}`)}); err != nil {
		t.Fatal(err)
	}
	if len(historyPage(t, store).Revisions) != 3 {
		t.Fatal("third party created instruction history")
	}
	changed := `{"models":[{"slug":"gpt-test","base_instructions":"updated baseline","description":"new metadata","model_messages":{"instructions_template":"template v2","instructions_variables":{"persona":"direct"},"permissions":null},"other":{"custom_instructions":"custom"},"new_instructions":"added"}]}`
	if err = store.UpdateAccountCatalog(ctx, account.ID, json.RawMessage(changed), []string{"gpt-test"}, "2.0.0"); err != nil {
		t.Fatal(err)
	}
	changes, err := store.InstructionHistory(ctx, InstructionHistoryFilter{Kind: "changed"})
	if err != nil || len(changes.Revisions) != 3 {
		t.Fatalf("changed models/catalog missing: %#v %v", changes, err)
	}
	for _, revision := range changes.Revisions {
		if revision.Model != "gpt-test" {
			continue
		}
		if len(revision.Changes) != 5 || revision.ClientVersion != "2.0.0" || revision.PreviousClientVersion != "1.1.0" {
			t.Fatalf("field/version loss: %#v", revision)
		}
		for _, change := range revision.Changes {
			if strings.Contains(change.Path, "description") || strings.Contains(change.Path, "custom") {
				t.Fatal("unchanged field logged")
			}
			if change.Path == "/model_messages/permissions" {
				value, err := store.InstructionField(ctx, change.AfterHash)
				if err != nil || value.Kind != "json" || value.Text != "null" {
					t.Fatalf("null conflated with empty string: %#v %v", value, err)
				}
			}
		}
	}
	status, err = store.InstructionStatus(ctx, 0)
	if err != nil || status.Unseen != 3 {
		t.Fatalf("notifications: %#v %v", status, err)
	}
	cleared, err := store.InstructionStatus(ctx, status.LatestChangeID)
	if err != nil || cleared.Unseen != 0 {
		t.Fatal("seen marker ignored")
	}
	var beforeCount int
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM instruction_contents`).Scan(&beforeCount); err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateAccountCatalog(ctx, account.ID, json.RawMessage(original), []string{"gpt-test", "model-b"}, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	var afterCount int
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM instruction_contents`).Scan(&afterCount)
	if beforeCount != afterCount {
		t.Fatal("reverted or shared content was duplicated")
	}
	if err = store.DeleteAccount(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	if len(historyPage(t, store).Revisions) != 9 {
		t.Fatal("account deletion erased history")
	}
}

func TestInstructionBaselineUpgradeAndRestartPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.db")
	store := testStore(t, path)
	ctx := context.Background()
	input := accountInput("legacy", "access")
	input.RawCatalogSnapshot = nil
	account, _, err := store.PutAccount(ctx, input, false)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"models":[{"slug":"gpt-test","base_instructions":"previously cached"}]}`)
	// Simulate an installation predating instruction history.
	if _, err = store.db.Exec(`INSERT INTO catalog_snapshots(provider,account_id,raw_json,fetched_at) VALUES('openai',?,?,?)`, account.ID, raw, time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := testStore(t, path)
	page := historyPage(t, reopened)
	if len(page.Revisions) != 1 || page.Revisions[0].Kind != "baseline" {
		t.Fatal("cached baseline missing")
	}
	field, err := reopened.InstructionField(ctx, page.Revisions[0].Changes[0].AfterHash)
	if err != nil || field.Text != "previously cached" {
		t.Fatalf("baseline content: %#v %v", field, err)
	}
	if err = reopened.UpdateAccountCatalog(ctx, account.ID, json.RawMessage(`{"models":[{"slug":"gpt-test","base_instructions":"new"}]}`), []string{"gpt-test"}); err != nil {
		t.Fatal(err)
	}
	_ = reopened.Close()
	again := testStore(t, path)
	if len(historyPage(t, again).Revisions) != 2 {
		t.Fatal("restart lost or duplicated history")
	}
}

func TestInstructionHistoryAtomicityAndConcurrentRefreshes(t *testing.T) {
	store := testStore(t, ":memory:")
	ctx := context.Background()
	original := `{"models":[{"slug":"gpt-test","base_instructions":"original"}]}`
	account := historyAccount(t, store, original)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_instruction_history BEFORE INSERT ON instruction_revisions BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountCatalog(ctx, account.ID, json.RawMessage(`{"models":[{"slug":"gpt-test","base_instructions":"new"}]}`), []string{"gpt-test"}); err == nil {
		t.Fatal("failed history write accepted")
	}
	current, err := store.Account(ctx, account.ID, false)
	if err != nil || string(current.RawCatalogSnapshot) != original {
		t.Fatal("catalog committed without its history")
	}
	if _, err = store.db.Exec(`DROP TRIGGER fail_instruction_history`); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	failures := make(chan error, 10)
	for i := 0; i < 10; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			failures <- store.UpdateAccountCatalog(ctx, account.ID, json.RawMessage(fmt.Sprintf(`{"models":[{"slug":"gpt-test","base_instructions":"version %d"}]}`, i)), []string{"gpt-test"})
		}(i)
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	page := historyPage(t, store)
	if len(page.Revisions) != 11 {
		t.Fatal("concurrent observations lost")
	}
	for i := 0; i < len(page.Revisions)-1; i++ {
		if page.Revisions[i].Changes[0].BeforeHash != page.Revisions[i+1].Changes[0].AfterHash {
			t.Fatal("concurrent revision chain skipped an observation")
		}
	}
}

func TestInstructionHistoryPreservesMissingEmptyFieldsAndDuplicateModels(t *testing.T) {
	store := testStore(t, ":memory:")
	ctx := context.Background()
	account := historyAccount(t, store, `{"models":[{"slug":"gpt-test","base_instructions":"","include_skills_usage_instructions":false,"model_messages":{}},{"slug":"gpt-test","base_instructions":"second"}]}`)
	if len(historyPage(t, store).Revisions) != 2 {
		t.Fatal("duplicate model definition hidden")
	}
	if err := store.UpdateAccountCatalog(ctx, account.ID, json.RawMessage(`{"models":[{"slug":"gpt-test","model_messages":{"permissions":"new policy"}}]}`), []string{"gpt-test"}); err != nil {
		t.Fatal(err)
	}
	page, _ := store.InstructionHistory(ctx, InstructionHistoryFilter{Kind: "changed", Model: "gpt-test"})
	if len(page.Revisions) != 1 || len(page.Revisions[0].Changes) != 4 {
		t.Fatalf("missing/control/container changes hidden: %#v", page)
	}
	for _, change := range page.Revisions[0].Changes {
		if change.Path == "/base_instructions" {
			value, err := store.InstructionField(ctx, change.BeforeHash)
			if err != nil || !value.Present || value.Text != "" || change.AfterHash != "" {
				t.Fatal("empty string confused with absent field")
			}
		}
	}
}
