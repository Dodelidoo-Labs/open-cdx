package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/ollama"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openrouter"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

type Manager struct {
	store *storage.Store
}

type BuildResult struct {
	Raw       []byte
	Hash      string
	Conflicts map[string]string
}

func NewManager(store *storage.Store) *Manager {
	return &Manager{store: store}
}

func (manager *Manager) BuildForDevice(ctx context.Context, deviceID, codexVersion string) (BuildResult, error) {
	accounts, err := manager.store.Accounts(ctx, false)
	if err != nil {
		return BuildResult{}, err
	}
	nativeEntries, conflicts, err := mergeNativeAccounts(accounts)
	if err != nil {
		return BuildResult{}, err
	}
	entries := append([]json.RawMessage(nil), nativeEntries...)
	for _, providerName := range []string{"openrouter", "ollama"} {
		providerEntries, providerErr := manager.translatedEntries(ctx, providerName)
		if providerErr != nil && !errors.Is(providerErr, storage.ErrNotFound) {
			return BuildResult{}, providerErr
		}
		entries = append(entries, providerEntries...)
	}
	if len(entries) == 0 {
		return BuildResult{}, errors.New("no validated models are available; add an OpenAI account or compatible provider first")
	}
	raw, err := json.Marshal(struct {
		Models []json.RawMessage `json:"models"`
	}{Models: entries})
	if err != nil {
		return BuildResult{}, err
	}
	hash, err := manager.store.PutMergedCatalog(ctx, deviceID, codexVersion, raw)
	if err != nil {
		return BuildResult{}, err
	}
	if err = manager.store.ReplaceConflicts(ctx, conflicts); err != nil {
		return BuildResult{}, err
	}
	return BuildResult{Raw: raw, Hash: hash, Conflicts: conflicts}, nil
}

func (manager *Manager) translatedEntries(ctx context.Context, providerName string) ([]json.RawMessage, error) {
	provider, err := manager.store.Provider(ctx, providerName, true)
	if err != nil {
		return nil, err
	}
	if !provider.Enabled {
		return nil, nil
	}
	snapshots, err := manager.store.CatalogSnapshots(ctx, providerName)
	if err != nil {
		return nil, err
	}
	if len(snapshots) == 0 {
		return nil, storage.ErrNotFound
	}
	var entries []json.RawMessage
	var excluded map[string]string
	switch providerName {
	case "openrouter":
		client, clientErr := openrouter.New(nil, provider.BaseURL, provider.APIKey)
		if clientErr != nil {
			return nil, clientErr
		}
		discovery, parseErr := ParseOpenRouterSnapshot(snapshots[0].Raw)
		if parseErr != nil {
			return nil, parseErr
		}
		entries, excluded, err = client.TranslateCatalog(discovery)
	case "ollama":
		client, clientErr := ollama.New(nil, provider.BaseURL, true)
		if clientErr != nil {
			return nil, clientErr
		}
		discovery, parseErr := ParseTranslatedSnapshot(snapshots[0].Raw)
		if parseErr != nil {
			return nil, parseErr
		}
		entries, excluded, err = client.TranslateCatalog(discovery)
	default:
		return nil, fmt.Errorf("unsupported catalog provider %q", providerName)
	}
	if err != nil {
		return nil, err
	}
	exclusions := make([]storage.CatalogExclusion, 0, len(excluded))
	for modelID, reason := range excluded {
		exclusions = append(exclusions, storage.CatalogExclusion{Provider: providerName, ModelID: modelID, Reason: reason})
	}
	if err = manager.store.ReplaceExclusions(ctx, providerName, exclusions); err != nil {
		return nil, err
	}
	return entries, nil
}

func (manager *Manager) RefreshOpenRouter(ctx context.Context, client *openrouter.Client) error {
	discovery, err := client.DiscoverModels(ctx, providers.Credential{}, "")
	if err != nil {
		return err
	}
	if err = manager.store.PutCatalogSnapshot(ctx, storage.CatalogSnapshot{
		Provider: "openrouter", Raw: discovery.Raw, ETag: discovery.ETag, FetchedAt: discovery.FetchedAt,
	}); err != nil {
		return err
	}
	return nil
}

func (manager *Manager) RefreshOllama(ctx context.Context, client *ollama.Client) error {
	discovery, err := client.DiscoverModels(ctx, providers.Credential{}, "")
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(discovery)
	if err != nil {
		return err
	}
	return manager.store.PutCatalogSnapshot(ctx, storage.CatalogSnapshot{
		Provider: "ollama", Raw: encoded, FetchedAt: discovery.FetchedAt,
	})
}

// ValidateProviderCatalog translates a stored discovery snapshot and records
// conservative exclusion reasons without requiring a device catalog build.
func (manager *Manager) ValidateProviderCatalog(ctx context.Context, providerName string) error {
	_, err := manager.translatedEntries(ctx, providerName)
	return err
}

func (manager *Manager) OpenRouterCapabilities(ctx context.Context, modelID string) (map[string]bool, error) {
	model, err := manager.OpenRouterModel(ctx, modelID)
	if err != nil {
		return nil, err
	}
	return model.Capabilities, nil
}

func (manager *Manager) OpenRouterModel(ctx context.Context, modelID string) (providers.DiscoveredModel, error) {
	snapshots, err := manager.store.CatalogSnapshots(ctx, "openrouter")
	if err != nil || len(snapshots) == 0 {
		return providers.DiscoveredModel{}, storage.ErrNotFound
	}
	discovery, err := ParseOpenRouterSnapshot(snapshots[0].Raw)
	if err != nil {
		return providers.DiscoveredModel{}, err
	}
	for _, model := range discovery.Models {
		if model.ID == modelID {
			if reason := openrouter.CompatibilityReason(model); reason != "" {
				return providers.DiscoveredModel{}, fmt.Errorf("OpenRouter model is excluded: %s", reason)
			}
			return model, nil
		}
	}
	return providers.DiscoveredModel{}, storage.ErrNotFound
}

func (manager *Manager) OllamaCapabilities(ctx context.Context, modelID string) (map[string]bool, error) {
	snapshots, err := manager.store.CatalogSnapshots(ctx, "ollama")
	if err != nil || len(snapshots) == 0 {
		return nil, storage.ErrNotFound
	}
	discovery, err := ParseTranslatedSnapshot(snapshots[0].Raw)
	if err != nil {
		return nil, err
	}
	for _, model := range discovery.Models {
		if model.ID == modelID {
			if reason := ollama.CompatibilityReason(model); reason != "" {
				return nil, fmt.Errorf("Ollama model is excluded: %s", reason)
			}
			return model.Capabilities, nil
		}
	}
	return nil, storage.ErrNotFound
}

func ParseOpenRouterSnapshot(raw []byte) (providers.Discovery, error) {
	discovery, err := openrouter.ParseDiscovery(raw)
	if err != nil {
		return providers.Discovery{}, err
	}
	discovery.FetchedAt = time.Now().UTC()
	return discovery, nil
}

func ParseTranslatedSnapshot(raw []byte) (providers.Discovery, error) {
	var discovery providers.Discovery
	if err := json.Unmarshal(raw, &discovery); err != nil {
		return providers.Discovery{}, err
	}
	return discovery, nil
}

func mergeNativeAccounts(accounts []storage.Account) ([]json.RawMessage, map[string]string, error) {
	type chosenEntry struct {
		raw       json.RawMessage
		primary   bool
		accountID string
	}
	chosen := make(map[string]chosenEntry)
	conflicts := make(map[string]string)
	for _, account := range accounts {
		if account.Paused || account.Status != "ready" || len(account.RawCatalogSnapshot) == 0 {
			continue
		}
		entitled := make(map[string]struct{}, len(account.EntitledModels))
		for _, modelID := range account.EntitledModels {
			entitled[modelID] = struct{}{}
		}
		var snapshot struct {
			Models []json.RawMessage `json:"models"`
		}
		if err := json.Unmarshal(account.RawCatalogSnapshot, &snapshot); err != nil {
			return nil, nil, errors.New("stored native catalog snapshot was invalid")
		}
		for _, rawModel := range snapshot.Models {
			var identity struct {
				Slug string `json:"slug"`
			}
			if err := json.Unmarshal(rawModel, &identity); err != nil || identity.Slug == "" {
				return nil, nil, errors.New("stored native catalog entry omitted its slug")
			}
			if _, eligible := entitled[identity.Slug]; !eligible {
				continue
			}
			existing, found := chosen[identity.Slug]
			if !found {
				chosen[identity.Slug] = chosenEntry{raw: append(json.RawMessage(nil), rawModel...), primary: account.Primary, accountID: account.ID}
				continue
			}
			if !jsonEqual(existing.raw, rawModel) {
				conflicts[identity.Slug] = "account catalogs contain conflicting complete definitions; one upstream definition was retained"
				if account.Primary && !existing.primary {
					chosen[identity.Slug] = chosenEntry{raw: append(json.RawMessage(nil), rawModel...), primary: true, accountID: account.ID}
				}
			}
		}
	}
	identifiers := make([]string, 0, len(chosen))
	for modelID := range chosen {
		identifiers = append(identifiers, modelID)
	}
	sort.SliceStable(identifiers, func(left, right int) bool {
		leftPriority := modelPriority(chosen[identifiers[left]].raw)
		rightPriority := modelPriority(chosen[identifiers[right]].raw)
		if leftPriority == rightPriority {
			return identifiers[left] < identifiers[right]
		}
		return leftPriority < rightPriority
	})
	entries := make([]json.RawMessage, 0, len(identifiers))
	for _, modelID := range identifiers {
		entries = append(entries, chosen[modelID].raw)
	}
	return entries, conflicts, nil
}

func jsonEqual(left, right []byte) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil &&
		bytes.Equal(mustCanonical(leftValue), mustCanonical(rightValue))
}

func mustCanonical(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func modelPriority(raw []byte) int {
	var model struct {
		Priority int `json:"priority"`
	}
	_ = json.Unmarshal(raw, &model)
	return model.Priority
}

func RouteIdentity(modelID string) (provider, upstream string) {
	for _, prefix := range []string{"openrouter/", "ollama/"} {
		if strings.HasPrefix(modelID, prefix) {
			return strings.TrimSuffix(prefix, "/"), strings.TrimPrefix(modelID, prefix)
		}
	}
	return "openai", modelID
}
