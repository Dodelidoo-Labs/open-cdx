package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers"
)

// ResetTicket is display metadata, never an OAuth credential. A missing ID
// means the service supplied a count without individual credit details.
type ResetTicket struct {
	ID        string     `json:"id,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type resetCreditDetails struct {
	AvailableCount int `json:"available_count"`
	Credits        []struct {
		ID        string     `json:"id"`
		Status    string     `json:"status"`
		ExpiresAt *time.Time `json:"expires_at"`
	} `json:"credits"`
}

// ResetTickets honors the authoritative count, including when details are
// capped, but removes credits whose known expiration has passed since polling.
func ResetTickets(raw []byte, now time.Time) []ResetTicket {
	var payload struct {
		Credits *resetCreditDetails `json:"rate_limit_reset_credits"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Credits == nil {
		return nil
	}
	details := payload.Credits
	if details.AvailableCount <= 0 || details.AvailableCount > 1000 {
		return nil
	}
	count := details.AvailableCount
	tickets := make([]ResetTicket, 0, count)
	for _, credit := range details.Credits {
		if credit.Status != "available" {
			continue
		}
		if credit.ExpiresAt != nil && !now.Before(*credit.ExpiresAt) {
			count--
			continue
		}
		tickets = append(tickets, ResetTicket{ID: credit.ID, ExpiresAt: credit.ExpiresAt})
	}
	count = max(0, count)
	if len(tickets) > count {
		tickets = tickets[:count]
	}
	for len(tickets) < count {
		tickets = append(tickets, ResetTicket{})
	}
	return tickets
}

func (client *Client) collectResetDetails(ctx context.Context, credential providers.Credential, raw []byte) []byte {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.ChatGPTBase+"/wham/rate-limit-reset-credits", nil)
	if err != nil {
		return raw
	}
	client.addAccountAuth(request.Header, credential)
	response, err := client.HTTP.Do(request)
	if err != nil {
		return raw
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return raw
	}
	var decoded struct {
		resetCreditDetails
		Count *int `json:"available_count"`
	}
	if decodeLimitedJSON(response.Body, &decoded) != nil || decoded.Count == nil || *decoded.Count < 0 {
		return raw
	}
	details := decoded.resetCreditDetails
	details.AvailableCount = *decoded.Count
	var payload map[string]json.RawMessage
	if json.Unmarshal(raw, &payload) != nil {
		return raw
	}
	payload["rate_limit_reset_credits"], err = json.Marshal(details)
	if err != nil {
		return raw
	}
	merged, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return merged
}

type ConsumeResetRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	CreditID       string `json:"credit_id,omitempty"`
}

func (input ConsumeResetRequest) Validate() error {
	if strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 128 || len(input.CreditID) > 512 {
		return errors.New("a non-empty idempotency_key (at most 128 characters) and an optional credit_id (at most 512 characters) are required")
	}
	return nil
}

// ConsumeReset uses the same account-scoped HTTP contract as Codex app-server.
// The key is sent in the body as redeem_request_id, not an HTTP header.
func (client *Client) ConsumeReset(ctx context.Context, credential providers.Credential, input ConsumeResetRequest) (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	body, _ := json.Marshal(struct {
		RequestID string `json:"redeem_request_id"`
		CreditID  string `json:"credit_id,omitempty"`
	}{input.IdempotencyKey, input.CreditID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.ChatGPTBase+"/wham/rate-limit-reset-credits/consume", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	client.addAccountAuth(request.Header, credential)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return "", errors.New("reset result is unknown; retry with the same idempotency key")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("reset redemption returned HTTP %d; retry with the same idempotency key", response.StatusCode)
	}
	var payload struct {
		Code string `json:"code"`
	}
	if decodeLimitedJSON(response.Body, &payload) != nil {
		return "", errors.New("reset response was invalid; retry with the same idempotency key")
	}
	switch payload.Code {
	case "reset":
		return "reset", nil
	case "already_redeemed":
		return "alreadyRedeemed", nil
	case "nothing_to_reset":
		return "nothingToReset", nil
	case "no_credit":
		return "noCredit", nil
	default:
		return "", errors.New("reset response was unrecognized; retry with the same idempotency key")
	}
}
