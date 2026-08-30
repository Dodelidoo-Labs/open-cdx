package routing

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

func TestPrimaryThenConfiguredFallbackOrderWithAffinity(t *testing.T) {
	box, _ := secure.NewBox(bytes.Repeat([]byte{8}, 32))
	store, err := storage.Open(":memory:", box)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, _, err := store.PutAccount(context.Background(), routeAccount("first", 90, []string{"gpt-shared", "gpt-first"}), false)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := store.PutAccount(context.Background(), routeAccount("second", 1, []string{"gpt-shared", "gpt-second"}), false)
	if err != nil {
		t.Fatal(err)
	}
	third, _, err := store.PutAccount(context.Background(), routeAccount("third", 50, []string{"gpt-shared", "gpt-third"}), false)
	if err != nil {
		t.Fatal(err)
	}
	selector := NewSelector(store, bytes.Repeat([]byte{4}, 32))
	selected, err := selector.SelectNative(context.Background(), "device", "gpt-shared", "thread-1", "")
	if err != nil || selected.Account.ID != first.ID {
		t.Fatalf("primary eligible account not selected: %#v %v", selected, err)
	}
	if err = store.ReorderAccounts(context.Background(), []string{first.ID, third.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkAccountExhausted(context.Background(), first.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	selected, err = selector.Rebind(context.Background(), "device", "gpt-shared", "thread-1", first.ID)
	if err != nil || selected.Account.ID != third.ID {
		t.Fatalf("configured first fallback was not selected: %#v %v", selected, err)
	}

	// Existing thread affinity remains sticky, but a new thread follows the
	// newly configured fallback sequence.
	if err = store.ReorderAccounts(context.Background(), []string{first.ID, second.ID, third.ID}); err != nil {
		t.Fatal(err)
	}
	selected, err = selector.SelectNative(context.Background(), "device", "gpt-shared", "thread-1", "")
	if err != nil || selected.Account.ID != third.ID {
		t.Fatalf("sticky account affinity was not maintained: %#v %v", selected, err)
	}
	selected, err = selector.SelectNative(context.Background(), "device", "gpt-shared", "thread-2", "")
	if err != nil || selected.Account.ID != second.ID {
		t.Fatalf("new request did not use the first configured fallback: %#v %v", selected, err)
	}
	selected, err = selector.SelectNative(context.Background(), "device", "gpt-first", "other-thread", "")
	if !errors.Is(err, ErrNoEligibleAccount) || selected.Account.ID != "" {
		t.Fatal("router selected an account not available for the requested entitled model")
	}
}

func routeAccount(stable string, quota float64, models []string) storage.AccountInput {
	return storage.AccountInput{
		Credential:  storage.OpenAICredential{AccountID: stable, AccessToken: "access-" + stable, RefreshToken: "refresh-" + stable, IDToken: "id-" + stable, ExpiresAt: time.Now().Add(time.Hour)},
		MaskedEmail: stable + "@masked", Plan: "plus", Status: "ready", QuotaUsedPercent: quota,
		EntitledModels: models, RawCatalogSnapshot: []byte(`{"models":[]}`),
	}
}
