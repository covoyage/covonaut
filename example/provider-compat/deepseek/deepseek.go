package deepseek

import (
	"net/http"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/provider/openai"
)

const defaultBaseURL = "https://api.deepseek.com/v1"

type Config struct {
	APIKey  string
	BaseURL string
	Client  *http.Client
}

func New(cfg Config) agentcore.Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	return openai.New(openai.Config{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Client:  cfg.Client,
	})
}
