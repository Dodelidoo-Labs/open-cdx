package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultListenAddress  = ":8080"
	DefaultOpenAIBaseURL  = "https://chatgpt.com/backend-api/codex"
	DefaultChatGPTBaseURL = "https://chatgpt.com/backend-api"
	DefaultAuthIssuer     = "https://auth.openai.com"
	DefaultOAuthClientID  = "app_EMoamEEZ73f0CkXaXp7hrann"
)

type Router struct {
	ListenAddress          string
	DatabasePath           string
	MasterKeyFile          string
	AdminTokenFile         string
	PublicBaseURL          string
	OpenAIBaseURL          string
	ChatGPTBaseURL         string
	AuthIssuer             string
	OAuthClientID          string
	CredentialStorage      bool
	InsecureDevelopment    bool
	TrustProxyHeaders      bool
	CatalogRefreshInterval time.Duration
	QuotaRefreshInterval   time.Duration
	HTTPTimeout            time.Duration
}

func RouterFromFlags(args []string) (Router, error) {
	defaults := Router{
		ListenAddress:          envOr("OPENCODEX_LISTEN", DefaultListenAddress),
		DatabasePath:           envOr("OPENCODEX_DATABASE", "/var/lib/opencdx/router.db"),
		MasterKeyFile:          envOr("OPENCODEX_MASTER_KEY_FILE", "/run/secrets/master_key"),
		AdminTokenFile:         envOr("OPENCODEX_ADMIN_TOKEN_FILE", "/run/secrets/admin_token"),
		PublicBaseURL:          envOr("OPENCODEX_PUBLIC_URL", "http://127.0.0.1:8080"),
		OpenAIBaseURL:          envOr("OPENCODEX_OPENAI_BASE_URL", DefaultOpenAIBaseURL),
		ChatGPTBaseURL:         envOr("OPENCODEX_CHATGPT_BASE_URL", DefaultChatGPTBaseURL),
		AuthIssuer:             envOr("OPENCODEX_AUTH_ISSUER", DefaultAuthIssuer),
		OAuthClientID:          envOr("OPENCODEX_OAUTH_CLIENT_ID", DefaultOAuthClientID),
		CredentialStorage:      envBool("OPENCODEX_CREDENTIAL_STORAGE", true),
		InsecureDevelopment:    envBool("OPENCODEX_INSECURE_DEV", false),
		TrustProxyHeaders:      envBool("OPENCODEX_TRUST_PROXY_HEADERS", false),
		CatalogRefreshInterval: envDuration("OPENCODEX_CATALOG_REFRESH_INTERVAL", time.Hour),
		QuotaRefreshInterval:   envDuration("OPENCODEX_QUOTA_REFRESH_INTERVAL", 5*time.Minute),
		HTTPTimeout:            envDuration("OPENCODEX_HTTP_TIMEOUT", 5*time.Minute),
	}
	flags := flag.NewFlagSet("routerd", flag.ContinueOnError)
	flags.StringVar(&defaults.ListenAddress, "listen", defaults.ListenAddress, "HTTP listen address")
	flags.StringVar(&defaults.DatabasePath, "database", defaults.DatabasePath, "SQLite database path")
	flags.StringVar(&defaults.MasterKeyFile, "master-key-file", defaults.MasterKeyFile, "master encryption key secret file")
	flags.StringVar(&defaults.AdminTokenFile, "admin-token-file", defaults.AdminTokenFile, "administrator token secret file")
	flags.StringVar(&defaults.PublicBaseURL, "public-url", defaults.PublicBaseURL, "public router URL")
	flags.BoolVar(&defaults.InsecureDevelopment, "insecure-dev", defaults.InsecureDevelopment, "allow plaintext non-loopback URLs for development")
	if err := flags.Parse(args); err != nil {
		return Router{}, err
	}
	if err := defaults.Validate(); err != nil {
		return Router{}, err
	}
	return defaults, nil
}

func (config Router) Validate() error {
	if config.DatabasePath == "" {
		return errors.New("database path is required")
	}
	publicURL, err := url.Parse(config.PublicBaseURL)
	if err != nil || publicURL.Host == "" {
		return errors.New("public URL must be an absolute URL")
	}
	if publicURL.Scheme != "https" && !config.InsecureDevelopment && !isLoopbackHost(publicURL.Hostname()) {
		return errors.New("public URL must use HTTPS unless insecure development mode is explicitly enabled")
	}
	if publicURL.User != nil || publicURL.RawQuery != "" || publicURL.Fragment != "" || publicURL.RawPath != "" || (publicURL.Path != "" && publicURL.Path != "/") {
		return errors.New("public URL must be an origin without credentials, a path, query parameters, or a fragment")
	}
	for label, rawURL := range map[string]string{
		"OpenAI base URL":  config.OpenAIBaseURL,
		"ChatGPT base URL": config.ChatGPTBaseURL,
		"OAuth issuer":     config.AuthIssuer,
	} {
		parsed, parseErr := url.Parse(rawURL)
		if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return fmt.Errorf("%s must be an absolute HTTPS URL", label)
		}
		if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("%s must not contain credentials, query parameters, or a fragment", label)
		}
	}
	if config.CredentialStorage && strings.TrimSpace(config.MasterKeyFile) == "" {
		return errors.New("credential storage requires a master key file")
	}
	return nil
}

func ReadMasterKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read master key: %w", err)
	}
	value := strings.TrimSpace(string(raw))
	for _, decode := range []func(string) ([]byte, error){base64.StdEncoding.DecodeString, hex.DecodeString} {
		if decoded, decodeErr := decode(value); decodeErr == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}
	if len(raw) == 32 {
		return raw, nil
	}
	return nil, errors.New("master key must contain exactly 32 raw bytes, or 32 bytes encoded as base64 or hexadecimal")
}

func ReadSecret(path, label string) (string, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	secret := strings.TrimSpace(string(raw))
	if len(secret) < 24 {
		return "", fmt.Errorf("%s must contain at least 24 characters", label)
	}
	return secret, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
