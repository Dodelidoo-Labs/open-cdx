package config

import (
	"testing"
	"time"
)

func TestDefaultCatalogRefreshIntervalIsHourly(t *testing.T) {
	t.Setenv("OPENCODEX_CATALOG_REFRESH_INTERVAL", "")
	config, err := RouterFromFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.CatalogRefreshInterval != time.Hour {
		t.Fatalf("catalog refresh interval=%s, expected 1h", config.CatalogRefreshInterval)
	}
}

func TestRouterRequiresHTTPSForNonLoopbackPublicURL(t *testing.T) {
	config := Router{
		DatabasePath: "/tmp/router.db", MasterKeyFile: "/tmp/key", CredentialStorage: true,
		PublicBaseURL: "http://192.0.2.10:8080", OpenAIBaseURL: DefaultOpenAIBaseURL,
		ChatGPTBaseURL: DefaultChatGPTBaseURL, AuthIssuer: DefaultAuthIssuer,
	}
	if err := config.Validate(); err == nil {
		t.Fatal("plaintext LAN public URL was accepted")
	}
	config.InsecureDevelopment = true
	if err := config.Validate(); err != nil {
		t.Fatalf("explicit insecure development mode was rejected: %v", err)
	}
	config.InsecureDevelopment = false
	config.PublicBaseURL = "https://router.example.com"
	if err := config.Validate(); err != nil {
		t.Fatalf("HTTPS production URL was rejected: %v", err)
	}
	config.PublicBaseURL = "https://router.example.com/hidden-prefix"
	if err := config.Validate(); err == nil {
		t.Fatal("public URL with an unsupported path prefix was accepted")
	}
}

func TestRouterRejectsNonHTTPSProviderInfrastructure(t *testing.T) {
	config := Router{
		DatabasePath: "/tmp/router.db", MasterKeyFile: "/tmp/key", CredentialStorage: true,
		PublicBaseURL: "https://router.example.com", OpenAIBaseURL: "http://chatgpt.example",
		ChatGPTBaseURL: DefaultChatGPTBaseURL, AuthIssuer: DefaultAuthIssuer,
	}
	if err := config.Validate(); err == nil {
		t.Fatal("plaintext OpenAI infrastructure URL was accepted")
	}
}

func TestTimeZoneIsExplicitAndValidated(t *testing.T) {
	t.Setenv("TZ", "Asia/Tokyo")
	t.Setenv("OPENCODEX_TIMEZONE", "")
	cfg, err := RouterFromFlags(nil)
	if err != nil || cfg.TimeZone != "UTC" {
		t.Fatalf("default=%#v err=%v", cfg, err)
	}
	t.Setenv("OPENCODEX_TIMEZONE", "America/Argentina/Buenos_Aires")
	cfg, err = RouterFromFlags(nil)
	if err != nil || cfg.TimeZone != "America/Argentina/Buenos_Aires" {
		t.Fatalf("setting=%#v err=%v", cfg, err)
	}
	cfg, err = RouterFromFlags([]string{"--timezone", "Europe/Zurich"})
	if err != nil || cfg.TimeZone != "Europe/Zurich" {
		t.Fatalf("flag=%#v err=%v", cfg, err)
	}
	for _, name := range []string{"Local", "invalid/zone"} {
		if _, err := RouterFromFlags([]string{"--timezone", name}); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}
