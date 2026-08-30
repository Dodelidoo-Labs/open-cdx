package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Dodelidoo-Labs/open-cdx/internal/providers"
)

const authorizeScope = "openid profile email offline_access api.connectors.read api.connectors.invoke"

type Client struct {
	HTTP          *http.Client
	Issuer        string
	ClientID      string
	ResponsesBase string
	ChatGPTBase   string
	Originator    string
}

func New(httpClient *http.Client, issuer, clientID, responsesBase, chatGPTBase string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Client{
		HTTP: httpClient, Issuer: strings.TrimRight(issuer, "/"), ClientID: clientID,
		ResponsesBase: strings.TrimRight(responsesBase, "/"), ChatGPTBase: strings.TrimRight(chatGPTBase, "/"),
		Originator: "codex_cli_rs",
	}
}

func (client *Client) AuthorizationURL(redirectURI, state, challenge string) (string, error) {
	if err := validateRedirectURI(redirectURI); err != nil {
		return "", err
	}
	endpoint, err := url.Parse(client.Issuer + "/oauth/authorize")
	if err != nil {
		return "", err
	}
	query := endpoint.Query()
	query.Set("response_type", "code")
	query.Set("client_id", client.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", authorizeScope)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("id_token_add_organizations", "true")
	query.Set("codex_cli_simplified_flow", "true")
	query.Set("state", state)
	query.Set("originator", client.Originator)
	query.Set("prompt", "login")
	query.Set("max_age", "0")
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (client *Client) Exchange(ctx context.Context, redirectURI, code, verifier string) (providers.Credential, error) {
	if err := validateRedirectURI(redirectURI); err != nil {
		return providers.Credential{}, err
	}
	values := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {client.ClientID},
		"code_verifier": {verifier},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.Issuer+"/oauth/token", strings.NewReader(values.Encode()))
	if err != nil {
		return providers.Credential{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return providers.Credential{}, fmt.Errorf("OAuth token exchange failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providers.Credential{}, fmt.Errorf("OAuth token exchange returned %s", response.Status)
	}
	var payload tokenResponse
	if err = decodeLimitedJSON(response.Body, &payload); err != nil {
		return providers.Credential{}, errors.New("OAuth token response was invalid")
	}
	return credentialFromTokenResponse(payload)
}

func (client *Client) Refresh(ctx context.Context, credential providers.Credential) (providers.Credential, error) {
	payload, err := json.Marshal(map[string]string{
		"client_id": client.ClientID, "grant_type": "refresh_token", "refresh_token": credential.RefreshToken,
	})
	if err != nil {
		return providers.Credential{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.Issuer+"/oauth/token", bytes.NewReader(payload))
	if err != nil {
		return providers.Credential{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return providers.Credential{}, errors.New("token refresh failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providers.Credential{}, &RefreshError{StatusCode: response.StatusCode}
	}
	var refreshed tokenResponse
	if err = decodeLimitedJSON(response.Body, &refreshed); err != nil {
		return providers.Credential{}, errors.New("token refresh response was invalid")
	}
	if refreshed.AccessToken == "" {
		return providers.Credential{}, errors.New("token refresh response omitted its access token")
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = credential.RefreshToken
	}
	if refreshed.IDToken == "" {
		// OAuth refresh responses are allowed to omit an ID token. Retain the
		// identity already validated at login instead of trying to re-parse an
		// old ID token whose exp claim may now be in the past.
		credential.AccessToken = refreshed.AccessToken
		credential.RefreshToken = refreshed.RefreshToken
		if refreshed.ExpiresIn > 0 {
			credential.ExpiresAt = time.Now().Add(time.Duration(refreshed.ExpiresIn) * time.Second).UTC()
		} else {
			credential.ExpiresAt = time.Now().Add(time.Hour).UTC()
		}
		return credential, nil
	}
	result, err := credentialFromTokenResponse(refreshed)
	if err != nil {
		return providers.Credential{}, err
	}
	if result.AccountID != credential.AccountID {
		return providers.Credential{}, errors.New("refreshed token identity changed")
	}
	return result, nil
}

func (client *Client) Identity(ctx context.Context, credential providers.Credential) (providers.Identity, error) {
	claims, err := parseClaims(credential.IDToken)
	if err != nil {
		return providers.Identity{}, errors.New("ID token did not contain a valid identity")
	}
	identity := claims.identity()
	if identity.AccountID == "" {
		return providers.Identity{}, errors.New("ID token did not contain a stable ChatGPT account identity claim")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.ChatGPTBase+"/wham/accounts/check", nil)
	if err != nil {
		return providers.Identity{}, err
	}
	client.addAccountAuth(request.Header, credential)
	response, err := client.HTTP.Do(request)
	if err != nil {
		return providers.Identity{}, errors.New("account identity validation failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return providers.Identity{}, fmt.Errorf("account identity validation returned %s", response.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return providers.Identity{}, errors.New("account identity validation returned an invalid response")
	}
	matched, err := accountCheckContains(raw, credential.AccountID)
	if err != nil || !matched {
		return providers.Identity{}, errors.New("validated account identity did not include the OAuth account")
	}
	return identity, nil
}

func (client *Client) DiscoverModels(ctx context.Context, credential providers.Credential, clientVersion string) (providers.Discovery, error) {
	endpoint, err := url.Parse(client.ResponsesBase + "/models")
	if err != nil {
		return providers.Discovery{}, err
	}
	query := endpoint.Query()
	query.Set("client_version", clientVersion)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return providers.Discovery{}, err
	}
	client.addAccountAuth(request.Header, credential)
	request.Header.Set("originator", client.Originator)
	request.Header.Set("version", clientVersion)
	response, err := client.HTTP.Do(request)
	if err != nil {
		return providers.Discovery{}, errors.New("OpenAI model discovery failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providers.Discovery{}, fmt.Errorf("OpenAI model discovery returned %s", response.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return providers.Discovery{}, err
	}
	models, err := ParseNativeModels(raw)
	if err != nil {
		return providers.Discovery{}, err
	}
	return providers.Discovery{Models: models, Raw: raw, ETag: response.Header.Get("ETag"), FetchedAt: time.Now().UTC()}, nil
}

func (client *Client) CollectQuota(ctx context.Context, credential providers.Credential) (providers.Quota, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.ChatGPTBase+"/wham/usage", nil)
	if err != nil {
		return providers.Quota{}, err
	}
	client.addAccountAuth(request.Header, credential)
	response, err := client.HTTP.Do(request)
	if err != nil {
		return providers.Quota{}, errors.New("quota collection failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providers.Quota{}, fmt.Errorf("quota collection returned %s", response.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return providers.Quota{}, err
	}
	return ParseQuota(raw)
}

func (client *Client) ResponsesURL(path string) (string, error) {
	switch path {
	case "/v1/responses":
		return client.ResponsesBase + "/responses", nil
	case "/v1/responses/compact":
		return client.ResponsesBase + "/responses/compact", nil
	default:
		return "", errors.New("unsupported OpenAI Responses path")
	}
}

func (client *Client) PrepareRequest(request *http.Request, credential providers.Credential, _ string) error {
	client.addAccountAuth(request.Header, credential)
	return nil
}

func (client *Client) Health(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, client.Issuer, nil)
	if err != nil {
		return err
	}
	response, err := client.HTTP.Do(request)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode >= 500 {
		return fmt.Errorf("OpenAI auth service returned %s", response.Status)
	}
	return nil
}

func (client *Client) addAccountAuth(headers http.Header, credential providers.Credential) {
	headers.Set("Authorization", "Bearer "+credential.AccessToken)
	headers.Set("ChatGPT-Account-ID", credential.AccountID)
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", "opencdx-router/0.1")
	}
	if credential.FedRAMP {
		headers.Set("X-OpenAI-Fedramp", "true")
	}
}

type RefreshError struct {
	StatusCode int
}

func (err *RefreshError) Error() string {
	return "refresh token was rejected"
}

func (err *RefreshError) Permanent() bool {
	return err.StatusCode == http.StatusBadRequest || err.StatusCode == http.StatusUnauthorized
}

type tokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func credentialFromTokenResponse(payload tokenResponse) (providers.Credential, error) {
	if payload.AccessToken == "" || payload.RefreshToken == "" || payload.IDToken == "" {
		return providers.Credential{}, errors.New("OAuth response omitted required tokens")
	}
	claims, err := parseClaims(payload.IDToken)
	if err != nil {
		return providers.Credential{}, errors.New("OAuth ID token was invalid")
	}
	identity := claims.identity()
	if identity.AccountID == "" {
		return providers.Credential{}, errors.New("OAuth ID token omitted the stable ChatGPT account ID")
	}
	expiresAt := claims.ExpiresAt()
	if payload.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second).UTC()
	}
	return providers.Credential{
		AccessToken: payload.AccessToken, RefreshToken: payload.RefreshToken, IDToken: payload.IDToken,
		AccountID: identity.AccountID, UserID: identity.UserID, ExpiresAt: expiresAt, FedRAMP: identity.FedRAMP,
	}, nil
}

type jwtClaims struct {
	Email   string                     `json:"email"`
	Expires int64                      `json:"exp"`
	Profile map[string]json.RawMessage `json:"https://api.openai.com/profile"`
	Auth    struct {
		Plan      string `json:"chatgpt_plan_type"`
		UserID    string `json:"chatgpt_user_id"`
		LegacyID  string `json:"user_id"`
		AccountID string `json:"chatgpt_account_id"`
		FedRAMP   bool   `json:"chatgpt_account_is_fedramp"`
	} `json:"https://api.openai.com/auth"`
}

func parseClaims(token string) (jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwtClaims{}, errors.New("invalid JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtClaims{}, err
	}
	var claims jwtClaims
	if err = json.Unmarshal(payload, &claims); err != nil {
		return jwtClaims{}, err
	}
	if claims.Expires != 0 && time.Now().Unix() >= claims.Expires {
		return jwtClaims{}, errors.New("expired JWT")
	}
	return claims, nil
}

func (claims jwtClaims) identity() providers.Identity {
	email := claims.Email
	if email == "" && claims.Profile != nil {
		_ = json.Unmarshal(claims.Profile["email"], &email)
	}
	userID := claims.Auth.UserID
	if userID == "" {
		userID = claims.Auth.LegacyID
	}
	return providers.Identity{
		AccountID: claims.Auth.AccountID, UserID: userID, Email: email, MaskedEmail: MaskEmail(email),
		Plan: claims.Auth.Plan, FedRAMP: claims.Auth.FedRAMP,
	}
}

func (claims jwtClaims) ExpiresAt() time.Time {
	if claims.Expires == 0 {
		return time.Now().Add(time.Hour).UTC()
	}
	return time.Unix(claims.Expires, 0).UTC()
}

func MaskEmail(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) != 2 || len(parts[0]) == 0 {
		return "account"
	}
	local := []rune(parts[0])
	masked := string(local[:1]) + "***"
	if len(local) > 1 {
		masked += string(local[len(local)-1:])
	}
	domainParts := strings.Split(parts[1], ".")
	domain := domainParts[0]
	if domain != "" {
		domain = string([]rune(domain)[:1]) + "***"
	}
	suffix := ""
	if len(domainParts) > 1 {
		suffix = "." + domainParts[len(domainParts)-1]
	}
	return masked + "@" + domain + suffix
}

func validateRedirectURI(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "localhost" || parsed.Path != "/auth/callback" {
		return errors.New("OAuth redirect URI must be the registered localhost callback")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || (port != 1455 && port != 1457) {
		return errors.New("OAuth callback must use registered port 1455 or 1457")
	}
	return nil
}

func accountCheckContains(raw []byte, expectedAccountID string) (bool, error) {
	var payload struct {
		Accounts         json.RawMessage `json:"accounts"`
		AccountOrdering  []string        `json:"account_ordering"`
		DefaultAccountID string          `json:"default_account_id"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false, err
	}
	if payload.DefaultAccountID == expectedAccountID {
		return true, nil
	}
	for _, accountID := range payload.AccountOrdering {
		if accountID == expectedAccountID {
			return true, nil
		}
	}
	var list []struct {
		ID string `json:"id"`
	}
	accounts := bytes.TrimSpace(payload.Accounts)
	if len(accounts) > 0 && accounts[0] == '[' {
		if err := json.Unmarshal(accounts, &list); err != nil {
			return false, err
		}
		for _, account := range list {
			if account.ID == expectedAccountID {
				return true, nil
			}
		}
		return false, nil
	}
	var keyed map[string]struct {
		Account struct {
			AccountID string `json:"account_id"`
		} `json:"account"`
	}
	if err := json.Unmarshal(accounts, &keyed); err != nil {
		return false, err
	}
	for key, account := range keyed {
		if key == expectedAccountID || account.Account.AccountID == expectedAccountID {
			return true, nil
		}
	}
	return false, nil
}

func decodeLimitedJSON(reader io.Reader, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 2<<20))
	return decoder.Decode(destination)
}
