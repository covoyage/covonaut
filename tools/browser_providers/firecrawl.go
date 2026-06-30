package browser_providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

type FirecrawlProvider struct {
	apiKey     string
	apiURL     string
	ttl        int
	httpClient *http.Client
}

func NewFirecrawlProvider() *FirecrawlProvider {
	ttl := 300
	if envTTL := os.Getenv("FIRECRAWL_BROWSER_TTL"); envTTL != "" {
		if t, err := strconv.Atoi(envTTL); err == nil {
			ttl = t
		}
	}

	apiURL := "https://api.firecrawl.dev"
	if envURL := os.Getenv("FIRECRAWL_API_URL"); envURL != "" {
		apiURL = envURL
	}

	return &FirecrawlProvider{
		apiKey:     os.Getenv("FIRECRAWL_API_KEY"),
		apiURL:     apiURL,
		ttl:        ttl,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *FirecrawlProvider) ProviderName() string {
	return "firecrawl"
}

func (p *FirecrawlProvider) IsConfigured() bool {
	return p.apiKey != ""
}

func (p *FirecrawlProvider) CreateSession(taskID string) (map[string]string, error) {
	reqBody := map[string]any{
		"ttl": p.ttl,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", p.apiURL+"/v2/browser", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("firecrawl API error %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	sessionID, _ := result["id"].(string)
	cdpURL, _ := result["cdpUrl"].(string)

	return map[string]string{
		"session_id": sessionID,
		"cdp_url":    cdpURL,
	}, nil
}

func (p *FirecrawlProvider) CloseSession(sessionID string) error {
	req, err := http.NewRequest("DELETE", fmt.Sprintf("%s/v2/browser/%s", p.apiURL, sessionID), nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("firecrawl close error %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (p *FirecrawlProvider) EmergencyCleanup(sessionID string) {
	_ = p.CloseSession(sessionID)
}
