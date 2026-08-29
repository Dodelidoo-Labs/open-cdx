package config

import "testing"

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
