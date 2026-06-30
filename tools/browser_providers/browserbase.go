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

type BrowserbaseProvider struct {
	apiKey      string
	projectID   string
	proxies     bool
	stealth     bool
	keepAlive   bool
	timeout     int
	httpClient  *http.Client
}

func NewBrowserbaseProvider() *BrowserbaseProvider {
	return &BrowserbaseProvider{
		apiKey:     os.Getenv("BROWSERBASE_API_KEY"),
		projectID:  os.Getenv("BROWSERBASE_PROJECT_ID"),
		proxies:    GetEnvBool("BROWSERBASE_PROXIES", true),
		stealth:    GetEnvBool("BROWSERBASE_ADVANCED_STEALTH", false),
		keepAlive:  GetEnvBool("BROWSERBASE_KEEP_ALIVE", true),
		timeout:    300000,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *BrowserbaseProvider) ProviderName() string {
	return "browserbase"
}

func (p *BrowserbaseProvider) IsConfigured() bool {
	return p.apiKey != "" && p.projectID != ""
}

func (p *BrowserbaseProvider) CreateSession(taskID string) (map[string]string, error) {
	if customTimeout := os.Getenv("BROWSERBASE_SESSION_TIMEOUT"); customTimeout != "" {
		if t, err := strconv.Atoi(customTimeout); err == nil {
			p.timeout = t
		}
	}

	reqBody := map[string]any{
		"projectId": p.projectID,
		"keepAlive": p.keepAlive,
		"timeout":   p.timeout,
	}

	if p.proxies {
		reqBody["proxies"] = true
	}
	if p.stealth {
		reqBody["advancedStealth"] = true
	}

	return p.createSessionWithBody(reqBody)
}

func (p *BrowserbaseProvider) createSessionWithBody(reqBody map[string]any) (map[string]string, error) {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.browserbase.com/v1/sessions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-bb-api-key", p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 402 {
		if p.proxies {
			reqBody["proxies"] = false
			return p.createSessionWithBody(reqBody)
		}
		if p.keepAlive {
			reqBody["keepAlive"] = false
			return p.createSessionWithBody(reqBody)
		}
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("browserbase API error %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	sessionID, _ := result["id"].(string)
	connectURL, _ := result["connectUrl"].(string)
	debugURL, _ := result["debuggerFullscreenUrl"].(string)

	cdpURL := connectURL
	if cdpURL == "" {
		cdpURL = debugURL
	}

	return map[string]string{
		"session_id": sessionID,
		"cdp_url":    cdpURL,
	}, nil
}

func (p *BrowserbaseProvider) CloseSession(sessionID string) error {
	reqBody := map[string]any{
		"status": "REQUEST_RELEASE",
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("https://api.browserbase.com/v1/sessions/%s", sessionID), bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-bb-api-key", p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("browserbase close error %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (p *BrowserbaseProvider) EmergencyCleanup(sessionID string) {
	_ = p.CloseSession(sessionID)
}
