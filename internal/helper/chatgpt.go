package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ChatGPTLaunch keeps the desktop app's native provider and login separate
// from the routed Codex home. It never reads or copies native credentials.
type ChatGPTLaunch struct {
	AppPath   string   `json:"app_path"`
	CodexHome string   `json:"codex_home"`
	Arguments []string `json:"arguments"`
}

func PlanChatGPTLaunch(ctx context.Context, configPath string) (ChatGPTLaunch, error) {
	if runtime.GOOS != "darwin" {
		return ChatGPTLaunch{}, errors.New("opening the ChatGPT desktop app is supported on macOS only")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ChatGPTLaunch{}, err
	}
	for _, app := range []string{filepath.Join(home, "Applications", "ChatGPT.app"), "/Applications/ChatGPT.app"} {
		plist := filepath.Join(app, "Contents", "Info.plist")
		identifier, err := exec.CommandContext(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :CFBundleIdentifier", plist).Output()
		// ChatGPT Classic has a different identity. Do not open it by mistake.
		if err != nil || strings.TrimSpace(string(identifier)) != "com.openai.codex" {
			continue
		}
		configPath, err = filepath.Abs(configPath)
		if err != nil {
			return ChatGPTLaunch{}, err
		}
		return chatGPTLaunch(app, filepath.Join(filepath.Dir(configPath), "chatgpt-home")), nil
	}
	return ChatGPTLaunch{}, errors.New("install the new ChatGPT app in /Applications or ~/Applications; ChatGPT Classic is not supported")
}

func chatGPTLaunch(app, home string) ChatGPTLaunch {
	return ChatGPTLaunch{AppPath: app, CodexHome: home, Arguments: []string{
		"-a", app,
		"--env", "CODEX_HOME=" + home,
		"--env", "CODEX_SQLITE_HOME=" + filepath.Join(home, "sqlite"),
		"--env", "CODEX_APP_SERVER_FORCE_CLI=1",
		"--env", "CODEX_CLI_PATH=",
	}}
}

func (launch ChatGPTLaunch) Open(ctx context.Context) error {
	// LaunchServices ignores a new environment when an app is already running.
	// Refuse instead of terminating someone's work or starting a second copy.
	output, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "comm=").Output()
	if err != nil {
		return errors.New("could not check whether ChatGPT is running")
	}
	if chatGPTIsRunning(string(output), launch.AppPath) {
		return errors.New("quit ChatGPT first, then choose Open ChatGPT Without Routing again; its launch environment cannot change while it is running")
	}
	if err = prepareChatGPTHome(launch.CodexHome); err != nil {
		return err
	}
	if err = exec.CommandContext(ctx, "/usr/bin/open", launch.Arguments...).Run(); err != nil {
		return fmt.Errorf("open ChatGPT: %w", err)
	}
	return nil
}

func chatGPTIsRunning(processes, app string) bool {
	prefix := filepath.Join(app, "Contents", "MacOS") + string(filepath.Separator)
	for _, command := range strings.Split(processes, "\n") {
		if strings.HasPrefix(strings.TrimSpace(command), prefix) {
			return true
		}
	}
	return false
}

func prepareChatGPTHome(home string) error {
	if !filepath.IsAbs(home) {
		return errors.New("ChatGPT home must be absolute")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(home)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("ChatGPT home must be a directory, not a symlink")
	}
	path := filepath.Join(home, "config.toml")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("ChatGPT configuration must be a regular file, not a symlink")
		}
		return nil // Keep settings the user or ChatGPT added on later launches.
	}
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString("# Native ChatGPT app only. CLI and IDE routing use their existing Codex home.\nmodel_provider = \"openai\"\ncli_auth_credentials_store = \"file\"\n")
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}
