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

	secure "github.com/opencdx/opencdx/internal/crypto"
)

const localTokenLifetime = 5 * time.Minute

func IssueLocalToken(secret string, now time.Time) (string, error) {
	if len(secret) < 32 {
		return "", errors.New("local token secret is unavailable")
	}
	nonce, err := secure.RandomURLSafe(18)
	if err != nil {
		return "", err
	}
	payload := fmt.Sprintf("v1.%d.%s", now.UTC().Add(localTokenLifetime).Unix(), nonce)
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
	if expires < nowUnix-30 || expires > nowUnix+int64((localTokenLifetime+time.Minute).Seconds()) {
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
