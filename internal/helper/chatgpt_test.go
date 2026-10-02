package helper

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestChatGPTLaunchScopesEnvironmentToInstalledApp(t *testing.T) {
	app := "/Applications/ChatGPT.app"
	home := "/Users/Test User/Library/Application Support/com.dodelidoo.opencdx/chatgpt-home"
	launch := chatGPTLaunch(app, home)
	want := []string{"-a", app, "--env", "CODEX_HOME=" + home,
		"--env", "CODEX_SQLITE_HOME=" + filepath.Join(home, "sqlite"),
		"--env", "CODEX_APP_SERVER_FORCE_CLI=1", "--env", "CODEX_CLI_PATH="}
	if !reflect.DeepEqual(launch.Arguments, want) {
		t.Fatalf("launch arguments = %#v", launch.Arguments)
	}
	// No -n (second instance), global launchctl environment, or shell parsing.
	if chatGPTIsRunning("/Applications/ChatGPT Classic.app/Contents/MacOS/ChatGPT\n/usr/bin/codex", app) {
		t.Fatal("unrelated client prevents native app launch")
	}
	if !chatGPTIsRunning("  /Applications/ChatGPT.app/Contents/MacOS/ChatGPT\n", app) {
		t.Fatal("running app would silently ignore the native environment")
	}
}

func TestChatGPTHomeDoesNotChangeRoutedHomeOrExistingNativeSettings(t *testing.T) {
	root := t.TempDir()
	routed := filepath.Join(root, ".codex")
	if err := os.MkdirAll(routed, 0o700); err != nil {
		t.Fatal(err)
	}
	routedConfig := []byte("model_provider = \"opencdx\"\nmodel_catalog_json = \"/tmp/router.json\"\n")
	if err := os.WriteFile(filepath.Join(routed, "config.toml"), routedConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "chatgpt-home")
	if err := prepareChatGPTHome(home); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Native ChatGPT app only. CLI and IDE routing use their existing Codex home.\nmodel_provider = \"openai\"\ncli_auth_credentials_store = \"file\"\n"
	if string(config) != want {
		t.Fatalf("native configuration = %q", config)
	}
	for _, name := range []string{"auth.json", "sessions", "catalog.json"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Fatalf("native home imported %s", name)
		}
	}
	custom := append(config, []byte("personality = \"pragmatic\"\n")...)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), custom, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareChatGPTHome(home); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{filepath.Join(home, "config.toml"): string(custom), filepath.Join(routed, "config.toml"): string(routedConfig)} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("configuration changed: %s: %q, %v", path, got, err)
		}
	}
}

func TestChatGPTHomeRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-home")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := prepareChatGPTHome(link); err == nil {
		t.Fatal("accepted a symlink home")
	}
	if err := os.Symlink(filepath.Join(root, "operator-config.toml"), filepath.Join(target, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if err := prepareChatGPTHome(target); err == nil {
		t.Fatal("accepted a symlink configuration")
	}
}
