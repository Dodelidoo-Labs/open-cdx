package routing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"sort"
	"time"

	"github.com/opencdx/opencdx/internal/storage"
)

var ErrNoEligibleAccount = errors.New("no active OpenAI account is entitled to the requested model")

type Selection struct {
	Account storage.Account
	ModelID string
}

type Selector struct {
	store  *storage.Store
	secret []byte
	now    func() time.Time
}

func NewSelector(store *storage.Store, affinitySecret []byte) *Selector {
	secret := append([]byte(nil), affinitySecret...)
	return &Selector{store: store, secret: secret, now: func() time.Time { return time.Now().UTC() }}
}

func (selector *Selector) SelectNative(ctx context.Context, deviceID, modelID, affinityValue, excludedAccount string) (Selection, error) {
	accounts, err := selector.store.Accounts(ctx, false)
	if err != nil {
		return Selection{}, err
	}
	eligible := make([]storage.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.ID == excludedAccount || !account.QuotaAvailable(selector.now()) || !contains(account.EntitledModels, modelID) {
			continue
		}
		eligible = append(eligible, account)
	}
	if len(eligible) == 0 {
		return Selection{}, ErrNoEligibleAccount
	}
	affinityHash := selector.affinityHash(deviceID, affinityValue)
	if len(affinityHash) > 0 {
		if accountID, affinityErr := selector.store.AffinityAccount(ctx, affinityHash, modelID); affinityErr == nil {
			for _, account := range eligible {
				if account.ID == accountID {
					return Selection{Account: account, ModelID: modelID}, nil
				}
			}
		}
	}
	sort.SliceStable(eligible, func(left, right int) bool {
		if eligible[left].Primary != eligible[right].Primary {
			return eligible[left].Primary
		}
		if eligible[left].RouteOrder != eligible[right].RouteOrder {
			return eligible[left].RouteOrder < eligible[right].RouteOrder
		}
		if !eligible[left].CreatedAt.Equal(eligible[right].CreatedAt) {
			return eligible[left].CreatedAt.Before(eligible[right].CreatedAt)
		}
		return eligible[left].ID < eligible[right].ID
	})
	selected := eligible[0]
	if len(affinityHash) > 0 {
		if err = selector.store.PutAffinity(ctx, affinityHash, modelID, selected.ID); err != nil {
			return Selection{}, err
		}
	}
	return Selection{Account: selected, ModelID: modelID}, nil
}

func (selector *Selector) Rebind(ctx context.Context, deviceID, modelID, affinityValue, exhaustedAccount string) (Selection, error) {
	if hash := selector.affinityHash(deviceID, affinityValue); len(hash) > 0 {
		_ = selector.store.DeleteAffinity(ctx, hash, modelID)
	}
	return selector.SelectNative(ctx, deviceID, modelID, affinityValue, exhaustedAccount)
}

func (selector *Selector) affinityHash(deviceID, value string) []byte {
	if value == "" || len(selector.secret) == 0 {
		return nil
	}
	mac := hmac.New(sha256.New, selector.secret)
	_, _ = mac.Write([]byte(deviceID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
