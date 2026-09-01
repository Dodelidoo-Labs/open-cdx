package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func DetectCodexVersion(ctx context.Context) string {
	executable := codexExecutable()
	if executable == "" {
		return "unknown"
	}
	versionContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(versionContext, executable, "--version").Output()
	if err != nil {
		return "unknown"
	}
	value := strings.TrimSpace(string(output))
	return normalizeCodexVersion(value)
}

type codexProcessInfo struct {
	Command   string
	StartedAt time.Time
}

func inspectCodexProcess(ctx context.Context, pid int) (codexProcessInfo, error) {
	if pid <= 0 {
		return codexProcessInfo{}, errors.New("invalid process ID")
	}
	command := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "lstart=", "-o", "comm=")
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	output, err := command.Output()
	if err != nil {
		return codexProcessInfo{}, err
	}
	return parseCodexProcessInfo(string(output))
}

func parseCodexProcessInfo(output string) (codexProcessInfo, error) {
	fields := strings.Fields(output)
	if len(fields) < 6 {
		return codexProcessInfo{}, errors.New("process details were incomplete")
	}
	startedAt, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(fields[:5], " "), time.Local)
	if err != nil {
		return codexProcessInfo{}, err
	}
	return codexProcessInfo{Command: strings.Join(fields[5:], " "), StartedAt: startedAt}, nil
}

func (process codexProcessInfo) isCodex() bool {
	name := strings.ToLower(filepath.Base(strings.TrimSpace(process.Command)))
	return name == "codex" || strings.HasPrefix(name, "codex-")
}

func normalizeCodexVersion(output string) string {
	for _, field := range strings.Fields(output) {
		candidate := strings.Trim(strings.TrimPrefix(field, "v"), "(),")
		candidate = strings.SplitN(candidate, "-", 2)[0]
		parts := strings.Split(candidate, ".")
		if len(parts) != 3 {
			continue
		}
		valid := true
		for _, part := range parts {
			if _, err := strconv.Atoi(part); err != nil {
				valid = false
				break
			}
		}
		if valid {
			return strings.Join(parts, ".")
		}
	}
	return "unknown"
}

func codexExecutable() string {
	if executable, err := exec.LookPath("codex"); err == nil {
		return executable
	}
	candidates := []string{"/opt/homebrew/bin/codex", "/usr/local/bin/codex"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "codex"),
			filepath.Join(home, "bin", "codex"),
			filepath.Join(home, ".npm-global", "bin", "codex"),
		)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}

func ConfigSnippet(config Config, helperPath string) (string, error) {
	if !filepath.IsAbs(config.CatalogPath) || !filepath.IsAbs(helperPath) {
		return "", errors.New("catalog and helper paths must be absolute")
	}
	return fmt.Sprintf(`model_provider = "opencdx"
model_catalog_json = %s

[model_providers.opencdx]
name = "OpenCDX Router"
base_url = %s
wire_api = "responses"
supports_websockets = false

[model_providers.opencdx.auth]
command = %s
args = ["token"]
timeout_ms = 5000
refresh_interval_ms = 300000
`, tomlQuote(config.CatalogPath), tomlQuote(config.LocalBaseURL()+"/v1"), tomlQuote(helperPath)), nil
}

func tomlQuote(value string) string { return strconv.Quote(value) }

func ExecutablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}
