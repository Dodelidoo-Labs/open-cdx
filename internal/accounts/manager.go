package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers"
	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
	"github.com/Dodelidoo-Labs/open-cdx/internal/storage"
)

const oauthLifetime = 5 * time.Minute

type Manager struct {
	store         *storage.Store
	client        *openai.Client
	refreshWindow time.Duration
	locksMutex    sync.Mutex
	refreshLocks  map[string]*sync.Mutex
}

type OAuthStart struct {
	TransactionID    string    `json:"transaction_id"`
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type OAuthComplete struct {
	Account  OAuthAccount `json:"account"`
	Replaced bool         `json:"replaced"`
}

type OAuthAccount struct {
	MaskedEmail      string    `json:"masked_email"`
	Plan             string    `json:"plan"`
	Status           string    `json:"status"`
	QuotaUsedPercent float64   `json:"quota_used_percent"`
	QuotaResetAt     time.Time `json:"quota_reset_at,omitempty"`
	Models           int       `json:"models"`
}

func NewManager(store *storage.Store, client *openai.Client) *Manager {
	return &Manager{store: store, client: client, refreshWindow: 5 * time.Minute, refreshLocks: make(map[string]*sync.Mutex)}
}

func (manager *Manager) StartOAuth(ctx context.Context, deviceID, redirectURI string) (OAuthStart, error) {
	_ = manager.store.PruneOAuthTransactions(ctx, time.Now().UTC())
	state, err := secure.RandomURLSafe(32)
	if err != nil {
		return OAuthStart{}, err
	}
	verifier, err := secure.RandomURLSafe(64)
	if err != nil {
		return OAuthStart{}, err
	}
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	expiresAt := time.Now().UTC().Add(oauthLifetime)
	transaction, err := manager.store.CreateOAuthTransaction(ctx, deviceID, state, verifier, redirectURI, expiresAt)
	if err != nil {
		return OAuthStart{}, err
	}
	authorizationURL, err := manager.client.AuthorizationURL(redirectURI, state, challenge)
	if err != nil {
		return OAuthStart{}, err
	}
	return OAuthStart{TransactionID: transaction.ID, AuthorizationURL: authorizationURL, ExpiresAt: expiresAt}, nil
}

func (manager *Manager) CompleteOAuth(ctx context.Context, deviceID, transactionID, state, code, clientVersion string, replace bool) (OAuthComplete, error) {
	if code == "" || len(code) > 8192 {
		return OAuthComplete{}, storage.ErrOAuthInvalid
	}
	transaction, err := manager.store.ConsumeOAuthTransaction(ctx, transactionID, deviceID, state, time.Now().UTC())
	if err != nil {
		return OAuthComplete{}, err
	}
	credential, err := manager.client.Exchange(ctx, transaction.RedirectURI, code, transaction.Verifier)
	if err != nil {
		return OAuthComplete{}, err
	}
	identity, quota, discovery, err := manager.validateCredential(ctx, credential, clientVersion)
	if err != nil {
		return OAuthComplete{}, err
	}
	input := storage.AccountInput{
		Credential: storageCredential(credential), MaskedEmail: identity.MaskedEmail, Plan: firstNonEmpty(quota.Plan, identity.Plan),
		Status: "ready", QuotaUsedPercent: quota.UsedPercent, QuotaResetAt: quota.ResetAt,
		ResetCredits: quota.ResetCredits, RawQuota: quota.Raw, RawCatalogSnapshot: discovery.Raw,
		EntitledModels: modelIDs(discovery.Models),
	}
	account, duplicate, err := manager.store.PutAccount(ctx, input, replace)
	if err != nil {
		return OAuthComplete{}, err
	}
	return OAuthComplete{Account: OAuthAccount{
		MaskedEmail: account.MaskedEmail, Plan: account.Plan, Status: account.Status,
		QuotaUsedPercent: account.QuotaUsedPercent, QuotaResetAt: account.QuotaResetAt, Models: len(account.EntitledModels),
	}, Replaced: duplicate}, nil
}

func (manager *Manager) validateCredential(ctx context.Context, credential providers.Credential, clientVersion string) (providers.Identity, providers.Quota, providers.Discovery, error) {
	type identityResult struct {
		value providers.Identity
		err   error
	}
	type quotaResult struct {
		value providers.Quota
		err   error
	}
	type discoveryResult struct {
		value providers.Discovery
		err   error
	}
	identityChannel := make(chan identityResult, 1)
	quotaChannel := make(chan quotaResult, 1)
	discoveryChannel := make(chan discoveryResult, 1)
	go func() {
		value, resultErr := manager.client.Identity(ctx, credential)
		identityChannel <- identityResult{value: value, err: resultErr}
	}()
	go func() {
		value, resultErr := manager.client.CollectQuota(ctx, credential)
		quotaChannel <- quotaResult{value: value, err: resultErr}
	}()
	go func() {
		value, resultErr := manager.client.DiscoverModels(ctx, credential, clientVersion)
		discoveryChannel <- discoveryResult{value: value, err: resultErr}
	}()
	identity := <-identityChannel
	quota := <-quotaChannel
	discovery := <-discoveryChannel
	if identity.err != nil {
		return providers.Identity{}, providers.Quota{}, providers.Discovery{}, identity.err
	}
	if quota.err != nil {
		return providers.Identity{}, providers.Quota{}, providers.Discovery{}, quota.err
	}
	if discovery.err != nil {
		return providers.Identity{}, providers.Quota{}, providers.Discovery{}, discovery.err
	}
	if identity.value.AccountID != credential.AccountID {
		return providers.Identity{}, providers.Quota{}, providers.Discovery{}, errors.New("validated account identity did not match OAuth identity")
	}
	return identity.value, quota.value, discovery.value, nil
}

func (manager *Manager) FreshCredential(ctx context.Context, accountID string) (providers.Credential, error) {
	account, err := manager.store.Account(ctx, accountID, true)
	if err != nil {
		return providers.Credential{}, err
	}
	credential := providerCredential(account.Credential)
	if time.Until(credential.ExpiresAt) > manager.refreshWindow {
		return credential, nil
	}
	lock := manager.refreshLock(accountID)
	lock.Lock()
	defer lock.Unlock()
	account, err = manager.store.Account(ctx, accountID, true)
	if err != nil {
		return providers.Credential{}, err
	}
	credential = providerCredential(account.Credential)
	if time.Until(credential.ExpiresAt) > manager.refreshWindow {
		return credential, nil
	}
	refreshed, err := manager.client.Refresh(ctx, credential)
	if err != nil {
		var refreshError *openai.RefreshError
		if errors.As(err, &refreshError) && refreshError.Permanent() {
			_ = manager.store.SetAccountStatus(ctx, accountID, "reauthentication_required", "OpenAI rejected the refresh token")
		}
		return providers.Credential{}, err
	}
	if err = manager.store.UpdateAccountCredential(ctx, accountID, storageCredential(refreshed)); err != nil {
		return providers.Credential{}, err
	}
	return refreshed, nil
}

func (manager *Manager) ForceRefreshCredential(ctx context.Context, accountID, rejectedAccessToken string) (providers.Credential, error) {
	lock := manager.refreshLock(accountID)
	lock.Lock()
	defer lock.Unlock()
	account, err := manager.store.Account(ctx, accountID, true)
	if err != nil {
		return providers.Credential{}, err
	}
	current := providerCredential(account.Credential)
	if rejectedAccessToken != "" && current.AccessToken != rejectedAccessToken {
		return current, nil
	}
	refreshed, err := manager.client.Refresh(ctx, current)
	if err != nil {
		var refreshError *openai.RefreshError
		if errors.As(err, &refreshError) && refreshError.Permanent() {
			_ = manager.store.SetAccountStatus(ctx, accountID, "reauthentication_required", "OpenAI rejected the refresh token")
		}
		return providers.Credential{}, err
	}
	if err = manager.store.UpdateAccountCredential(ctx, accountID, storageCredential(refreshed)); err != nil {
		return providers.Credential{}, err
	}
	return refreshed, nil
}

func (manager *Manager) RefreshQuota(ctx context.Context, accountID string) error {
	credential, err := manager.FreshCredential(ctx, accountID)
	if err != nil {
		return err
	}
	quota, err := manager.client.CollectQuota(ctx, credential)
	if err != nil {
		return err
	}
	return manager.store.UpdateAccountQuota(ctx, accountID, quota.Plan, quota.UsedPercent, quota.ResetAt, quota.ResetCredits, quota.Raw)
}

func (manager *Manager) RefreshCatalog(ctx context.Context, accountID, clientVersion string) error {
	credential, err := manager.FreshCredential(ctx, accountID)
	if err != nil {
		return err
	}
	discovery, err := manager.client.DiscoverModels(ctx, credential, clientVersion)
	if err != nil {
		return err
	}
	return manager.store.UpdateAccountCatalog(ctx, accountID, discovery.Raw, modelIDs(discovery.Models))
}

func (manager *Manager) RefreshQuotas(ctx context.Context) error {
	accounts, err := manager.store.Accounts(ctx, false)
	if err != nil {
		return err
	}
	var failures int
	for _, account := range accounts {
		if account.Paused {
			continue
		}
		if refreshErr := manager.RefreshQuota(ctx, account.ID); refreshErr != nil {
			failures++
			latest, latestErr := manager.store.Account(ctx, account.ID, false)
			if latestErr != nil || latest.Status != "reauthentication_required" {
				_ = manager.store.SetAccountStatus(ctx, account.ID, "degraded", "quota refresh failed")
			}
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d account quota refresh operations failed", failures)
	}
	return nil
}

func (manager *Manager) RefreshCatalogs(ctx context.Context, clientVersion string) error {
	accounts, err := manager.store.Accounts(ctx, false)
	if err != nil {
		return err
	}
	var failures int
	for _, account := range accounts {
		if account.Paused {
			continue
		}
		if refreshErr := manager.RefreshCatalog(ctx, account.ID, clientVersion); refreshErr != nil {
			failures++
			latest, latestErr := manager.store.Account(ctx, account.ID, false)
			if latestErr != nil || latest.Status != "reauthentication_required" {
				_ = manager.store.SetAccountStatus(ctx, account.ID, "degraded", "catalog refresh failed")
			}
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d account catalog refresh operations failed", failures)
	}
	return nil
}

func (manager *Manager) Client() *openai.Client {
	return manager.client
}

func (manager *Manager) refreshLock(accountID string) *sync.Mutex {
	manager.locksMutex.Lock()
	defer manager.locksMutex.Unlock()
	lock := manager.refreshLocks[accountID]
	if lock == nil {
		lock = &sync.Mutex{}
		manager.refreshLocks[accountID] = lock
	}
	return lock
}

func storageCredential(credential providers.Credential) storage.OpenAICredential {
	return storage.OpenAICredential{
		AccessToken: credential.AccessToken, RefreshToken: credential.RefreshToken, IDToken: credential.IDToken,
		AccountID: credential.AccountID, UserID: credential.UserID, ExpiresAt: credential.ExpiresAt, FedRAMP: credential.FedRAMP,
	}
}

func providerCredential(credential storage.OpenAICredential) providers.Credential {
	return providers.Credential{
		AccessToken: credential.AccessToken, RefreshToken: credential.RefreshToken, IDToken: credential.IDToken,
		AccountID: credential.AccountID, UserID: credential.UserID, ExpiresAt: credential.ExpiresAt, FedRAMP: credential.FedRAMP,
	}
}

func modelIDs(models []providers.DiscoveredModel) []string {
	identifiers := make([]string, 0, len(models))
	for _, model := range models {
		identifiers = append(identifiers, model.ID)
	}
	return identifiers
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "unknown"
}
