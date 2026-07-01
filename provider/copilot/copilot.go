// Package copilot implements the GitHub Copilot API provider.
//
// GitHub Copilot exposes an OpenAI-compatible chat completions endpoint
// authenticated via GitHub OAuth token.
package copilot

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/provider/chatcompat"
)

const defaultBaseURL = "https://api.githubcopilot.com"

// Config holds GitHub Copilot configuration.
type Config struct {
	Token   string // GitHub OAuth token; auto-detected if empty
	BaseURL string // optional override
}

// resolveToken attempts to get a GitHub token from config, env, or gh CLI.
func resolveToken(cfg Config) (string, error) {
	if cfg.Token != "" {
		return cfg.Token, nil
	}
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t, nil
	}
	if t := os.Getenv("COPILOT_GITHUB_TOKEN"); t != "" {
		return t, nil
	}
	if token, err := exec.Command("gh", "auth", "token").Output(); err == nil {
		return strings.TrimSpace(string(token)), nil
	}
	return "", fmt.Errorf("copilot: no GitHub token; set GITHUB_TOKEN or run 'gh auth login'")
}

// New creates a GitHub Copilot provider.
func New(cfg Config) (agentcore.Provider, error) {
	token, err := resolveToken(cfg)
	if err != nil {
		return nil, err
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return chatcompat.New(chatcompat.Config{
		APIKey:  token,
		BaseURL: baseURL,
		ExtraHeaders: map[string]string{
			"Copilot-Integration-Id": "vscode-chat",
			"Editor-Version":         "vscode/1.95.0",
		},
	}), nil
}
