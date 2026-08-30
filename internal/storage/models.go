package storage

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrDuplicateAccount    = errors.New("OpenAI account is already registered")
	ErrNotFound            = errors.New("record not found")
	ErrEnrollmentPending   = errors.New("device enrollment is pending")
	ErrEnrollmentRejected  = errors.New("device enrollment was rejected")
	ErrEnrollmentComplete  = errors.New("device enrollment token was already acknowledged")
	ErrDeviceRevoked       = errors.New("device is revoked")
	ErrDeviceNotDeletable  = errors.New("only rejected devices can be deleted")
	ErrOAuthInvalid        = errors.New("OAuth transaction is invalid, expired, or already used")
	ErrInvalidAccountOrder = errors.New("account order must contain every account exactly once")
)

type OpenAICredential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	IDToken      string    `json:"id_token"`
	AccountID    string    `json:"account_id"`
	UserID       string    `json:"user_id,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	FedRAMP      bool      `json:"fedramp,omitempty"`
}

type Account struct {
	ID                 string
	MaskedEmail        string
	Plan               string
	Status             string
	Paused             bool
	Primary            bool
	RouteOrder         int
	QuotaUsedPercent   float64
	QuotaResetAt       time.Time
	ResetCredits       int
	LastError          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Credential         OpenAICredential
	EntitledModels     []string
	RawQuota           json.RawMessage
	RawCatalogSnapshot json.RawMessage
}

func (account Account) QuotaAvailable(now time.Time) bool {
	if account.Paused || account.Status != "ready" {
		return false
	}
	return account.QuotaUsedPercent < 100 || (!account.QuotaResetAt.IsZero() && !now.Before(account.QuotaResetAt))
}

type AccountInput struct {
	Credential         OpenAICredential
	MaskedEmail        string
	Plan               string
	Status             string
	QuotaUsedPercent   float64
	QuotaResetAt       time.Time
	ResetCredits       int
	RawQuota           json.RawMessage
	RawCatalogSnapshot json.RawMessage
	EntitledModels     []string
}

type Device struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	ApprovedAt    time.Time `json:"approved_at,omitempty"`
	LastSeenAt    time.Time `json:"last_seen_at,omitempty"`
	RevokedAt     time.Time `json:"revoked_at,omitempty"`
	CatalogSynced time.Time `json:"catalog_synced_at,omitempty"`
}

type Enrollment struct {
	DeviceID         string `json:"device_id"`
	EnrollmentSecret string `json:"enrollment_secret"`
	Status           string `json:"status"`
	DeviceToken      string `json:"device_token,omitempty"`
}

type OAuthTransaction struct {
	ID          string
	DeviceID    string
	State       string
	Verifier    string
	RedirectURI string
	ExpiresAt   time.Time
}

type ProviderConfig struct {
	Name      string
	BaseURL   string
	Enabled   bool
	Health    string
	LastError string
	UpdatedAt time.Time
	APIKey    string
	Config    json.RawMessage
}

type CatalogSnapshot struct {
	Provider  string
	AccountID string
	ETag      string
	Raw       json.RawMessage
	FetchedAt time.Time
}

type CatalogExclusion struct {
	Provider string `json:"provider"`
	ModelID  string `json:"model_id"`
	Reason   string `json:"reason"`
}

type UsageAggregate struct {
	Day                   string
	Provider              string
	ModelID               string
	AccountID             string
	Source                string
	Routing               string
	Requests              int64
	InputTokens           int64
	CachedInputTokens     int64
	CacheWriteInputTokens int64
	OutputTokens          int64
	ReasoningOutputTokens int64
}

const (
	UsageSourceRouted     = "routed"
	UsageSourceReconciled = "reconciled"
	UsageRoutingRouted    = "routed"
	UsageRoutingNative    = "native"
)

type UsageReconciliation struct {
	ReconciledAt   time.Time
	FilesScanned   int
	EventsImported int
	RowsImported   int
}
