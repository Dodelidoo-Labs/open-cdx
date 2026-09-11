package accounts

import (
	"context"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers/openai"
)

type ResetResult struct {
	Outcome         string `json:"outcome"`
	QuotasRefreshed bool   `json:"quotas_refreshed"`
}

func (manager *Manager) ConsumeReset(ctx context.Context, accountID string, input openai.ConsumeResetRequest) (ResetResult, error) {
	if err := input.Validate(); err != nil {
		return ResetResult{}, err
	}
	lock := manager.refreshLock("quota:" + accountID)
	lock.Lock()
	defer lock.Unlock()
	credential, err := manager.FreshCredential(ctx, accountID)
	if err != nil {
		return ResetResult{}, err
	}
	outcome, err := manager.client.ConsumeReset(ctx, credential, input)
	if err != nil {
		return ResetResult{}, err
	}
	// Finish cache reconciliation even if the requesting HUD disconnects after
	// redemption. Never report a successful redemption as a failed attempt.
	refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	if outcome != "nothingToReset" {
		if err := manager.store.InvalidateAccountResetCredits(refreshCtx, accountID); err != nil {
			return ResetResult{Outcome: outcome}, nil
		}
	}
	err = manager.refreshQuota(refreshCtx, accountID)
	return ResetResult{Outcome: outcome, QuotasRefreshed: err == nil}, nil
}
