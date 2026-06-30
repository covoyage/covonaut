// Package mistral implements the Mistral Conversations API provider adapter.
//
// Mistral's Conversations API is wire-compatible with OpenAI's Chat
// Completions API (same request/response schema, SSE streaming format,
// and /v1/models endpoint).  This adapter delegates to the OpenAI
// provider with Mistral-specific defaults.
package mistral

import (
	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/provider/openai"
)

const defaultBaseURL = "https://api.mistral.ai/v1"

// Config holds Mistral-specific provider configuration.
type Config struct {
	APIKey  string // required
	BaseURL string // optional; defaults to api.mistral.ai
}

// New creates a Mistral Conversations API provider.
func New(cfg Config) agentcore.Provider {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return openai.New(openai.Config{
		APIKey:  cfg.APIKey,
		BaseURL: baseURL,
	})
}
