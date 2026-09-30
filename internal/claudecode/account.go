package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// LocalAccount reads the signed-in account's UUID and email from Claude
// Code's state file, the same values its telemetry reports. No other field,
// and no credential, is read. It returns false when the file has no login.
func LocalAccount() (Account, bool) {
	path := ""
	if configured := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configured != "" && filepath.IsAbs(configured) {
		path = filepath.Join(configured, ".claude.json")
	} else if home, err := os.UserHomeDir(); err == nil {
		path = filepath.Join(home, ".claude.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Account{}, false
	}
	return accountFromState(raw)
}

func accountFromState(raw []byte) (Account, bool) {
	var state struct {
		OAuthAccount *struct {
			AccountUUID  string `json:"accountUuid"`
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(raw, &state) != nil || state.OAuthAccount == nil {
		return Account{}, false
	}
	identity := AccountIdentity(state.OAuthAccount.AccountUUID)
	if identity == "" {
		return Account{}, false
	}
	return Account{Identity: identity, MaskedEmail: maskedEmail(state.OAuthAccount.EmailAddress)}, true
}
