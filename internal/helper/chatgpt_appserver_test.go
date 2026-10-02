package helper

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// An optional compatibility proof against the installed app's bundled binary.
// Neither the app nor a router is launched; both homes are temporary and use
// file authentication so the operator's credentials are never loaded.
func TestChatGPTHomeWithRealAppServer(t *testing.T) {
	binary := os.Getenv("OPENCODEX_CHATGPT_CODEX_BINARY")
	if binary == "" {
		t.Skip("set OPENCODEX_CHATGPT_CODEX_BINARY to test the app's bundled Codex")
	}
	for _, native := range []bool{false, true} {
		name := "routed"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "codex-home")
			if err := prepareChatGPTHome(home); err != nil {
				t.Fatal(err)
			}
			provider := "openai"
			if !native {
				provider = "opencdx"
				fixture := "model_provider = \"opencdx\"\ncli_auth_credentials_store = \"file\"\n" +
					"[model_providers.opencdx]\nname = \"Isolation Proof\"\nbase_url = \"http://127.0.0.1:1/v1\"\nwire_api = \"responses\"\n" +
					"[model_providers.opencdx.auth]\ncommand = \"/bin/echo\"\nargs = [\"synthetic-test-token\"]\n"
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(fixture), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "app-server")
			command.Dir = home // No project configuration from the checkout.
			command.Env = append(os.Environ(), "CODEX_HOME="+home, "CODEX_SQLITE_HOME="+filepath.Join(home, "sqlite"), "RUST_LOG=off")
			command.Stderr = io.Discard
			stdin, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
			encoder, decoder := json.NewEncoder(stdin), json.NewDecoder(stdout)
			request := func(id int, method string, params any, result any) {
				t.Helper()
				if err := encoder.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
					t.Fatal(err)
				}
				for {
					var response struct {
						ID     int
						Result json.RawMessage
						Error  json.RawMessage
					}
					if err := decoder.Decode(&response); err != nil {
						t.Fatal(err)
					}
					if response.ID != id {
						continue
					}
					if len(response.Error) != 0 {
						t.Fatalf("%s failed: %s", method, response.Error)
					}
					if result != nil {
						if err := json.Unmarshal(response.Result, result); err != nil {
							t.Fatal(err)
						}
					}
					return
				}
			}
			request(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "opencdx_isolation_proof", "version": "1"}}, nil)
			if err := encoder.Encode(map[string]string{"method": "initialized"}); err != nil {
				t.Fatal(err)
			}
			var config struct{ Config map[string]any }
			request(2, "config/read", map[string]bool{"includeLayers": false}, &config)
			if config.Config["model_provider"] != provider || config.Config["model_catalog_json"] != nil {
				t.Fatalf("wrong provider or leaked router catalog: %#v", config.Config)
			}
			var account struct {
				Account            any
				RequiresOpenAIAuth bool `json:"requiresOpenaiAuth"`
			}
			request(3, "account/read", map[string]bool{"refreshToken": false}, &account)
			if account.Account != nil || account.RequiresOpenAIAuth != native {
				t.Fatalf("unexpected native authentication: %#v", account)
			}
		})
	}
}
