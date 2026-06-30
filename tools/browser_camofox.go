package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type CamofoxClient struct {
	mu         sync.RWMutex
	baseURL    string
	userID     string
	sessionKey string
	adoptTab   bool
	managed    bool
	tabs       map[string]*CamofoxTab
	httpClient *http.Client
}

type CamofoxTab struct {
	TabID      string
	URL        string
	Title      string
	CreatedAt  time.Time
	LastActive time.Time
}

type CamofoxConfig struct {
	BaseURL            string
	UserID             string
	SessionKey         string
	AdoptExistingTab   bool
	ManagedPersistence bool
}

func NewCamofoxClient(cfg CamofoxConfig) *CamofoxClient {
	return &CamofoxClient{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		userID:     cfg.UserID,
		sessionKey: cfg.SessionKey,
		adoptTab:   cfg.AdoptExistingTab,
		managed:    cfg.ManagedPersistence,
		tabs:       make(map[string]*CamofoxTab),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func CamofoxFromEnv() *CamofoxClient {
	url := os.Getenv("CAMOFOX_URL")
	if url == "" {
		return nil
	}
	return NewCamofoxClient(CamofoxConfig{
		BaseURL:            url,
		UserID:             os.Getenv("CAMOFOX_USER_ID"),
		SessionKey:         os.Getenv("CAMOFOX_SESSION_KEY"),
		AdoptExistingTab:   os.Getenv("CAMOFOX_ADOPT_EXISTING_TAB") == "true",
		ManagedPersistence: false,
	})
}

func (c *CamofoxClient) IsConfigured() bool {
	return c.baseURL != ""
}

func (c *CamofoxClient) CreateTab(taskID string, url string) (*CamofoxTab, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.tabs[taskID]; ok {
		existing.LastActive = time.Now()
		return existing, nil
	}

	if c.adoptTab {
		if tab, err := c.adoptExistingTab(taskID); err == nil && tab != nil {
			c.tabs[taskID] = tab
			return tab, nil
		}
	}

	userID := c.userID
	if userID == "" {
		userID = "agent-" + taskID
	}

	sessionKey := c.sessionKey
	if sessionKey == "" {
		sessionKey = "session-" + taskID
	}

	reqBody := map[string]any{
		"userId":     userID,
		"sessionKey": sessionKey,
	}
	if url != "" {
		reqBody["url"] = url
	}

	resp, err := c.doJSON("POST", "/tabs", reqBody)
	if err != nil {
		return nil, fmt.Errorf("create tab failed: %w", err)
	}

	tabID, _ := resp["tabId"].(string)
	if tabID == "" {
		tabID, _ = resp["id"].(string)
	}

	tab := &CamofoxTab{
		TabID:      tabID,
		URL:        url,
		CreatedAt:  time.Now(),
		LastActive: time.Now(),
	}
	c.tabs[taskID] = tab

	return tab, nil
}

func (c *CamofoxClient) adoptExistingTab(taskID string) (*CamofoxTab, error) {
	userID := c.userID
	if userID == "" {
		userID = "agent-" + taskID
	}

	resp, err := c.doJSON("GET", "/tabs?userId="+userID, nil)
	if err != nil {
		return nil, err
	}

	tabs, ok := resp["tabs"].([]any)
	if !ok || len(tabs) == 0 {
		return nil, fmt.Errorf("no existing tabs found")
	}

	firstTab, _ := tabs[0].(map[string]any)
	tabID, _ := firstTab["tabId"].(string)
	if tabID == "" {
		tabID, _ = firstTab["id"].(string)
	}

	url, _ := firstTab["url"].(string)

	return &CamofoxTab{
		TabID:      tabID,
		URL:        url,
		CreatedAt:  time.Now(),
		LastActive: time.Now(),
	}, nil
}

func (c *CamofoxClient) Navigate(taskID string, url string) (string, error) {
	tab, ok := c.tabs[taskID]
	if !ok {
		return "", fmt.Errorf("no tab found for task %s", taskID)
	}

	_, err := c.doJSON("POST", fmt.Sprintf("/tabs/%s/navigate", tab.TabID), map[string]any{
		"url": url,
	})
	if err != nil {
		return "", fmt.Errorf("navigate failed: %w", err)
	}

	tab.URL = url
	tab.LastActive = time.Now()

	snapshot, err := c.GetSnapshot(taskID, false)
	if err != nil {
		return "", err
	}

	return snapshot, nil
}

func (c *CamofoxClient) GetSnapshot(taskID string, full bool) (string, error) {
	tab, ok := c.tabs[taskID]
	if !ok {
		return "", fmt.Errorf("no tab found for task %s", taskID)
	}

	params := ""
	if full {
		params = "?full=true"
	}

	resp, err := c.doJSON("GET", fmt.Sprintf("/tabs/%s/snapshot%s", tab.TabID, params), nil)
	if err != nil {
		return "", fmt.Errorf("snapshot failed: %w", err)
	}

	tree, _ := resp["tree"].(string)
	if tree == "" {
		tree, _ = resp["content"].(string)
	}

	tab.LastActive = time.Now()

	if len(tree) > 8000 {
		tree = tree[:8000] + "\n\n[... snapshot truncated to 8000 chars]"
	}

	return tree, nil
}

func (c *CamofoxClient) Click(taskID string, ref string) (string, error) {
	tab, ok := c.tabs[taskID]
	if !ok {
		return "", fmt.Errorf("no tab found for task %s", taskID)
	}

	_, err := c.doJSON("POST", fmt.Sprintf("/tabs/%s/click", tab.TabID), map[string]any{
		"ref": ref,
	})
	if err != nil {
		return "", fmt.Errorf("click failed: %w", err)
	}

	tab.LastActive = time.Now()

	return c.GetSnapshot(taskID, false)
}

func (c *CamofoxClient) Type(taskID string, ref string, text string) (string, error) {
	tab, ok := c.tabs[taskID]
	if !ok {
		return "", fmt.Errorf("no tab found for task %s", taskID)
	}

	_, err := c.doJSON("POST", fmt.Sprintf("/tabs/%s/type", tab.TabID), map[string]any{
		"ref":  ref,
		"text": text,
	})
	if err != nil {
		return "", fmt.Errorf("type failed: %w", err)
	}

	tab.LastActive = time.Now()

	return fmt.Sprintf("Typed \"%s\" into %s", text, ref), nil
}

func (c *CamofoxClient) Scroll(taskID string, direction string) (string, error) {
	tab, ok := c.tabs[taskID]
	if !ok {
		return "", fmt.Errorf("no tab found for task %s", taskID)
	}

	for i := 0; i < 5; i++ {
		_, err := c.doJSON("POST", fmt.Sprintf("/tabs/%s/scroll", tab.TabID), map[string]any{
			"direction": direction,
		})
		if err != nil {
			return "", fmt.Errorf("scroll failed: %w", err)
		}
	}

	tab.LastActive = time.Now()

	return c.GetSnapshot(taskID, false)
}

func (c *CamofoxClient) Back(taskID string) (string, error) {
	tab, ok := c.tabs[taskID]
	if !ok {
		return "", fmt.Errorf("no tab found for task %s", taskID)
	}

	_, err := c.doJSON("POST", fmt.Sprintf("/tabs/%s/back", tab.TabID), nil)
	if err != nil {
		return "", fmt.Errorf("back failed: %w", err)
	}

	tab.LastActive = time.Now()

	return c.GetSnapshot(taskID, false)
}

func (c *CamofoxClient) Press(taskID string, key string) (string, error) {
	tab, ok := c.tabs[taskID]
	if !ok {
		return "", fmt.Errorf("no tab found for task %s", taskID)
	}

	_, err := c.doJSON("POST", fmt.Sprintf("/tabs/%s/press", tab.TabID), map[string]any{
		"key": key,
	})
	if err != nil {
		return "", fmt.Errorf("press failed: %w", err)
	}

	tab.LastActive = time.Now()

	return fmt.Sprintf("Pressed key: %s", key), nil
}

func (c *CamofoxClient) Screenshot(taskID string) ([]byte, error) {
	tab, ok := c.tabs[taskID]
	if !ok {
		return nil, fmt.Errorf("no tab found for task %s", taskID)
	}

	resp, err := c.httpClient.Get(fmt.Sprintf("%s/tabs/%s/screenshot", c.baseURL, tab.TabID))
	if err != nil {
		return nil, fmt.Errorf("screenshot failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("screenshot returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read screenshot: %w", err)
	}

	tab.LastActive = time.Now()

	return data, nil
}

func (c *CamofoxClient) CloseTab(taskID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	tab, ok := c.tabs[taskID]
	if !ok {
		return nil
	}

	_, _ = c.doJSON("DELETE", fmt.Sprintf("/tabs/%s", tab.TabID), nil)
	delete(c.tabs, taskID)

	return nil
}

func (c *CamofoxClient) CloseSession() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.managed {
		for taskID := range c.tabs {
			delete(c.tabs, taskID)
		}
		return nil
	}

	userID := c.userID
	if userID == "" {
		userID = "agent-default"
	}

	_, _ = c.doJSON("DELETE", fmt.Sprintf("/sessions/%s", userID), nil)
	c.tabs = make(map[string]*CamofoxTab)

	return nil
}

func (c *CamofoxClient) GetVNCURL() string {
	resp, err := c.httpClient.Get(c.baseURL + "/health")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var health map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return ""
	}

	vnc, _ := health["vnc_url"].(string)
	return vnc
}

func (c *CamofoxClient) doJSON(method string, path string, body map[string]any) (map[string]any, error) {
	var reqBody io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("camofox API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result, nil
}
