package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type Credential struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	AccountID    string
	UserID       string
	ExpiresAt    time.Time
	FedRAMP      bool
}

type Identity struct {
	AccountID   string
	UserID      string
	Email       string
	MaskedEmail string
	Plan        string
	FedRAMP     bool
}

type Quota struct {
	Plan         string
	UsedPercent  float64
	ResetAt      time.Time
	ResetCredits int
	LimitReached bool
	Raw          json.RawMessage
}

type DiscoveredModel struct {
	ID                      string
	Raw                     json.RawMessage
	Capabilities            map[string]bool
	Context                 int64
	Description             string
	DisplayName             string
	InputModes              []string
	ReasoningEfforts        []string
	DefaultReasoningEffort  string
	ReasoningMandatory      bool
	ReasoningDefaultEnabled bool
}

type Discovery struct {
	Models    []DiscoveredModel
	Raw       json.RawMessage
	ETag      string
	FetchedAt time.Time
}

type OAuthAuthenticator interface {
	AuthorizationURL(redirectURI, state, challenge string) (string, error)
	Exchange(ctx context.Context, redirectURI, code, verifier string) (Credential, error)
	Identity(ctx context.Context, credential Credential) (Identity, error)
}

type CredentialRefresher interface {
	Refresh(ctx context.Context, credential Credential) (Credential, error)
}

type ModelDiscoverer interface {
	DiscoverModels(ctx context.Context, credential Credential, clientVersion string) (Discovery, error)
}

type CapabilityTranslator interface {
	TranslateCatalog(discovery Discovery) ([]json.RawMessage, map[string]string, error)
}

type QuotaCollector interface {
	CollectQuota(ctx context.Context, credential Credential) (Quota, error)
}

type ResponsesExecutor interface {
	ResponsesURL(path string) (string, error)
	PrepareRequest(request *http.Request, credential Credential, upstreamModel string) error
}

type HealthReporter interface {
	Health(ctx context.Context) error
}
