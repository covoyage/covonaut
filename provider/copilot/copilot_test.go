package copilot

import (
	"os"
	"testing"
)

func TestResolveToken_FromConfig(t *testing.T) {
	token, err := resolveToken(Config{Token: "gh_token_123"})
	if err != nil {
		t.Fatal(err)
	}
	if token != "gh_token_123" {
		t.Fatalf("token = %q", token)
	}
}

func TestResolveToken_FromEnv(t *testing.T) {
	os.Setenv("GITHUB_TOKEN", "env_token")
	defer os.Unsetenv("GITHUB_TOKEN")

	token, err := resolveToken(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if token != "env_token" {
		t.Fatalf("token = %q", token)
	}
}

func TestResolveToken_FromCopilotEnv(t *testing.T) {
	os.Setenv("COPILOT_GITHUB_TOKEN", "copilot_token")
	defer os.Unsetenv("COPILOT_GITHUB_TOKEN")

	token, err := resolveToken(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if token != "copilot_token" {
		t.Fatalf("token = %q", token)
	}
}

func TestNew_NoToken(t *testing.T) {
	os.Unsetenv("GITHUB_TOKEN")
	os.Unsetenv("COPILOT_GITHUB_TOKEN")

	_, err := New(Config{})
	if err == nil {
		t.Fatal("expected error without token")
	}
}

func TestNew_WithToken(t *testing.T) {
	provider, err := New(Config{Token: "test_token"})
	if err != nil {
		t.Fatal(err)
	}
	if provider == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestDefaultBaseURL(t *testing.T) {
	if defaultBaseURL != "https://api.githubcopilot.com" {
		t.Fatalf("unexpected default URL: %s", defaultBaseURL)
	}
}
