package catalog

import (
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
		// This client only translates an already stored catalog snapshot and
		// performs no network I/O. Live refreshes and routed requests enforce the
		// persisted Allow HTTP policy at their network boundaries.
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
	entries, details, err := mergeNativeAccountsDetailed(accounts)
	if err != nil {
		return nil, nil, err
	}
	conflicts := make(map[string]string, len(details))
	for _, detail := range details {
		conflicts[detail.Model] = fmt.Sprintf("%d differing fields across %d account definitions; one complete upstream definition was retained", len(detail.Fields), len(detail.Sources))
	}
	return entries, conflicts, nil
}

func mergeNativeAccountsDetailed(accounts []storage.Account) ([]json.RawMessage, []NativeConflict, error) {
	definitions := make(map[string][]nativeDefinition)
	chosen := make(map[string]int)
	for _, account := range accounts {
		if account.Paused || account.Status != "ready" || len(account.RawCatalogSnapshot) == 0 {
			continue
		}
		entitled := make(map[string]bool, len(account.EntitledModels))
		for _, modelID := range account.EntitledModels {
			entitled[modelID] = true
		}
		var snapshot struct {
			Models []json.RawMessage `json:"models"`
		}
		if err := json.Unmarshal(account.RawCatalogSnapshot, &snapshot); err != nil {
			return nil, nil, errors.New("stored native catalog snapshot was invalid")
		}
		for index, rawModel := range snapshot.Models {
			var identity struct {
				Slug string `json:"slug"`
			}
			if err := json.Unmarshal(rawModel, &identity); err != nil || identity.Slug == "" {
				return nil, nil, errors.New("stored native catalog entry omitted its slug")
			}
			if !entitled[identity.Slug] {
				continue
			}
			value, err := decodeDefinition(rawModel)
			if err != nil {
				return nil, nil, errors.New("stored native catalog entry was invalid")
			}
			sources := definitions[identity.Slug]
			if len(sources) == 0 || (account.Primary && !sources[chosen[identity.Slug]].source.Primary) {
				chosen[identity.Slug] = len(sources)
			}
			definitions[identity.Slug] = append(sources, nativeDefinition{raw: rawModel, value: value,
				source: ConflictSource{AccountID: account.ID, Account: account.MaskedEmail, Plan: account.Plan,
					Primary: account.Primary, CatalogEntry: index + 1}})
		}
	}
	identifiers := make([]string, 0, len(definitions))
	for modelID := range definitions {
		identifiers = append(identifiers, modelID)
	}
	sort.Strings(identifiers)
	conflicts := []NativeConflict{}
	for _, modelID := range identifiers {
		if len(definitions[modelID]) < 2 {
			continue
		}
		conflict := definitionConflict(modelID, definitions[modelID], chosen[modelID])
		if len(conflict.Fields) > 0 {
			conflicts = append(conflicts, conflict)
		}
	}
	sort.SliceStable(identifiers, func(left, right int) bool {
		leftID, rightID := identifiers[left], identifiers[right]
		leftPriority := modelPriority(definitions[leftID][chosen[leftID]].raw)
		rightPriority := modelPriority(definitions[rightID][chosen[rightID]].raw)
		if leftPriority == rightPriority {
			return leftID < rightID
		}
		return leftPriority < rightPriority
	})
	entries := make([]json.RawMessage, 0, len(identifiers))
	for _, modelID := range identifiers {
		entries = append(entries, definitions[modelID][chosen[modelID]].raw)
	}
	return entries, conflicts, nil
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
