package browser_providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type BrowserUseProvider struct {
	apiKey     string
	httpClient *http.Client
}

func NewBrowserUseProvider() *BrowserUseProvider {
	return &BrowserUseProvider{
		apiKey:     os.Getenv("BROWSER_USE_API_KEY"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *BrowserUseProvider) ProviderName() string {
	return "browser_use"
}

func (p *BrowserUseProvider) IsConfigured() bool {
	return p.apiKey != ""
}

func (p *BrowserUseProvider) CreateSession(taskID string) (map[string]string, error) {
	reqBody := map[string]any{
		"idempotency_key": "agent-" + taskID,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.browser-use.com/v3/browsers", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("browser_use API error %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	sessionID, _ := result["id"].(string)
	cdpURL, _ := result["cdpUrl"].(string)
	if cdpURL == "" {
		cdpURL, _ = result["connectUrl"].(string)
	}

	return map[string]string{
		"session_id": sessionID,
		"cdp_url":    cdpURL,
	}, nil
}

func (p *BrowserUseProvider) CloseSession(sessionID string) error {
	reqBody := map[string]any{
		"action": "stop",
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequest("PATCH", fmt.Sprintf("https://api.browser-use.com/v3/browsers/%s", sessionID), bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("browser_use close error %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (p *BrowserUseProvider) EmergencyCleanup(sessionID string) {
	_ = p.CloseSession(sessionID)
}
