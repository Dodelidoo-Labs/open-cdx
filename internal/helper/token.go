package helper

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	secure "github.com/Dodelidoo-Labs/open-cdx/internal/crypto"
)

const LocalTokenLifetime = 5 * time.Minute

func IssueLocalToken(secret string, now time.Time) (string, error) {
	if len(secret) < 32 {
		return "", errors.New("local token secret is unavailable")
	}
	nonce, err := secure.RandomURLSafe(18)
	if err != nil {
		return "", err
	}
	payload := fmt.Sprintf("v1.%d.%s", now.UTC().Add(LocalTokenLifetime).Unix(), nonce)
	return payload + "." + signLocal(secret, payload), nil
}

func VerifyLocalToken(secret, token string, now time.Time) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "v1" || len(parts[2]) < 16 {
		return false
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	nowUnix := now.UTC().Unix()
	if expires < nowUnix-30 || expires > nowUnix+int64((LocalTokenLifetime+time.Minute).Seconds()) {
		return false
	}
	payload := strings.Join(parts[:3], ".")
	expected := signLocal(secret, payload)
	return hmac.Equal([]byte(expected), []byte(parts[3]))
}

func signLocal(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ScopedTokenLifetime covers Claude Code's default 29-minute refresh of
// otelHeadersHelper output with a margin for sleep and clock adjustments.
const ScopedTokenLifetime = time.Hour

// IssueScopedToken returns a credential accepted only by the endpoint for its
// scope. Its separate prefix keeps it from authenticating inference.
func IssueScopedToken(secret, scope string, now time.Time) (string, error) {
	if len(secret) < 32 {
		return "", errors.New("local token secret is unavailable")
	}
	if scope == "" || strings.ContainsAny(scope, ".") {
		return "", errors.New("invalid token scope")
	}
	nonce, err := secure.RandomURLSafe(18)
	if err != nil {
		return "", err
	}
	payload := fmt.Sprintf("s1.%s.%d.%s", scope, now.UTC().Add(ScopedTokenLifetime).Unix(), nonce)
	return payload + "." + signLocal(secret, payload), nil
}

func VerifyScopedToken(secret, scope, token string, now time.Time) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 5 || parts[0] != "s1" || parts[1] != scope || len(parts[3]) < 16 {
		return false
	}
	expires, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return false
	}
	nowUnix := now.UTC().Unix()
	if expires < nowUnix-30 || expires > nowUnix+int64((ScopedTokenLifetime+time.Minute).Seconds()) {
		return false
	}
	payload := strings.Join(parts[:4], ".")
	return hmac.Equal([]byte(signLocal(secret, payload)), []byte(parts[4]))
}
