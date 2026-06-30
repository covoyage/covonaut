package tools

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// WebSearchOperations defines pluggable operations for the web search tool.
type WebSearchOperations interface {
	Search(query string, count int) ([]SearchResult, error)
}

// SearchResult represents a single search result.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// chainedWebSearchOperations auto-selects credentialed backends, then keyless fallbacks.
type chainedWebSearchOperations struct {
	cfg *WebSearchToolConfig
}

func (c chainedWebSearchOperations) Search(query string, count int) ([]SearchResult, error) {
	if count <= 0 {
		count = 10
	}
	_, results, err := searchWithFallbackChain(newSearchHTTPClient(), query, count, c.cfg)
	return results, err
}

// WebSearchToolConfig configures the web search tool.
type WebSearchToolConfig struct {
	Operations       WebSearchOperations
	MaxBytes         int64
	Limit            int
	Provider         string // explicit provider id or "auto"
	APIURL           string
	APIKey           string
	GeminiAPIKey     string // optional injected key (e.g. reuse chat provider credentials)
	GeminiModel      string
	ChatProviderName string
}

func searchSerpAPI(client *http.Client, query string, count int, apiKey string) ([]SearchResult, error) {
	endpoint := "https://serpapi.com/search.json"
	q := url.Values{}
	q.Set("q", query)
	q.Set("engine", envOrDefault("SERPAPI_ENGINE", "google"))
	q.Set("api_key", apiKey)
	q.Set("num", fmt.Sprintf("%d", count))
	if location := os.Getenv("SERPAPI_LOCATION"); location != "" {
		q.Set("location", location)
	}
	var resp struct {
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic_results"`
	}
	if err := getJSON(client, endpoint+"?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(resp.Organic))
	for _, r := range resp.Organic {
		results = append(results, SearchResult{Title: r.Title, URL: r.Link, Snippet: r.Snippet})
	}
	return limitResults(results, count), nil
}

func searchBrave(client *http.Client, query string, count int, apiKey string) ([]SearchResult, error) {
	endpoint := "https://api.search.brave.com/res/v1/web/search"
	q := url.Values{}
	q.Set("q", query)
	q.Set("count", fmt.Sprintf("%d", count))
	if country := os.Getenv("BRAVE_SEARCH_COUNTRY"); country != "" {
		q.Set("country", country)
	}
	headers := map[string]string{
		"X-Subscription-Token": apiKey,
		"Accept":               "application/json",
	}
	var resp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := getJSON(client, endpoint+"?"+q.Encode(), headers, &resp); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(resp.Web.Results))
	for _, r := range resp.Web.Results {
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Description})
	}
	return limitResults(results, count), nil
}

func searchTavily(client *http.Client, query string, count int, apiKey string) ([]SearchResult, error) {
	body := map[string]any{
		"api_key":        apiKey,
		"query":          query,
		"max_results":    count,
		"search_depth":   envOrDefault("TAVILY_SEARCH_DEPTH", "basic"),
		"include_answer": false,
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, "https://api.tavily.com/search", strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var resp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := doJSONRequest(client, req, &resp); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return limitResults(results, count), nil
}

func searchSearXNG(client *http.Client, query string, count int, baseURL string) ([]SearchResult, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/search"
	q := url.Values{}
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("language", envOrDefault("SEARXNG_LANGUAGE", "zh-CN"))
	var resp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := getJSON(client, endpoint+"?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return limitResults(results, count), nil
}

func searchGenericJSON(client *http.Client, apiURL, query string, count int, apiKey string) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("query", query)
	q.Set("count", fmt.Sprintf("%d", count))
	q.Set("num", fmt.Sprintf("%d", count))
	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
		headers["X-API-Key"] = apiKey
	}
	var resp struct {
		Results []SearchResult `json:"results"`
		Items   []SearchResult `json:"items"`
	}
	sep := "?"
	if strings.Contains(apiURL, "?") {
		sep = "&"
	}
	if err := getJSON(client, apiURL+sep+q.Encode(), headers, &resp); err != nil {
		return nil, err
	}
	if len(resp.Results) > 0 {
		return limitResults(resp.Results, count), nil
	}
	return limitResults(resp.Items, count), nil
}

func searchBingRSS(client *http.Client, query string, count int) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("format", "rss")
	q.Set("mkt", envOrDefault("WEB_SEARCH_MARKET", "zh-CN"))

	req, err := http.NewRequest(http.MethodGet, "https://www.bing.com/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultSearchUserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Dnt", "1")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("default search returned HTTP %d", resp.StatusCode)
	}
	return parseBingRSSResults(resp.Body, count)
}

func parseBingRSSResults(source io.Reader, count int) ([]SearchResult, error) {
	var resp struct {
		Channel struct {
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}

	if err := xml.NewDecoder(source).Decode(&resp); err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(resp.Channel.Items))
	for _, item := range resp.Channel.Items {
		results = append(results, SearchResult{
			Title:   cleanSearchText(item.Title),
			URL:     strings.TrimSpace(item.Link),
			Snippet: cleanSearchText(item.Description),
		})
	}
	return limitResults(results, count), nil
}

var htmlTagRE = regexp.MustCompile(`<[^>]+>`)

func cleanSearchText(s string) string {
	s = html.UnescapeString(s)
	s = htmlTagRE.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func getJSON(client *http.Client, rawURL string, headers map[string]string, target any) error {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return doJSONRequest(client, req, target)
}

func doJSONRequest(client *http.Client, req *http.Request, target any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("search backend returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func limitResults(results []SearchResult, count int) []SearchResult {
	filtered := results[:0]
	for _, r := range results {
		if r.Title == "" && r.URL == "" && r.Snippet == "" {
			continue
		}
		filtered = append(filtered, r)
		if len(filtered) >= count {
			break
		}
	}
	return filtered
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func (c *WebSearchToolConfig) defaults() {
	if c.Operations == nil {
		c.Operations = chainedWebSearchOperations{cfg: c}
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = 50 * 1024
	}
	if c.Limit <= 0 {
		c.Limit = 10
	}
}

// WebSearchToolInput is the JSON arguments for the web search tool.
type WebSearchToolInput struct {
	Query string `json:"query"`
	Count *int   `json:"count,omitempty"`
}

// WebSearchToolDetails carries truncation metadata.
type WebSearchToolDetails struct {
	Provider   string            `json:"provider,omitempty"`
	Truncation *TruncationResult `json:"truncation,omitempty"`
}

// NewWebSearchTool creates a web search tool.
func NewWebSearchTool(cfg *WebSearchToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &WebSearchToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "web_search",
		Description: fmt.Sprintf("Search the web. Auto-selects the best available backend (API keys when configured, otherwise DuckDuckGo then Bing RSS). Returns titles, URLs, and snippets. Output is truncated to %d results or %s (whichever is hit first).", cfg.Limit, FormatSize(cfg.MaxBytes)),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query string"},
				"count": map[string]any{"type": "integer", "description": fmt.Sprintf("Number of results to return (default: %d)", cfg.Limit)},
			},
			"required": []any{"query"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			// Coerce string count to integer: some LLMs pass count as "10" instead of 10
			args = coerceCountToInt(args)

			var input WebSearchToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			if input.Query == "" {
				return resultErrf("query is required")
			}

			count := cfg.Limit
			if input.Count != nil && *input.Count > 0 {
				count = *input.Count
			}

			var (
				results  []SearchResult
				provider string
				err      error
			)
			if chain, ok := cfg.Operations.(chainedWebSearchOperations); ok {
				provider, results, err = searchWithFallbackChain(newSearchHTTPClient(), input.Query, count, chain.cfg)
			} else {
				results, err = cfg.Operations.Search(input.Query, count)
			}
			if err != nil {
				return resultErrf("search failed: %w", err)
			}

			var outputLines []string
			for i, r := range results {
				if i >= count {
					break
				}
				outputLines = append(outputLines, fmt.Sprintf("%d. %s\n   %s\n   %s", i+1, r.Title, r.URL, r.Snippet))
			}

			rawOutput := strings.Join(outputLines, "\n\n")
			truncation := TruncateHead(rawOutput, TruncationOptions{MaxBytes: int(cfg.MaxBytes), MaxLines: 1<<31 - 1})
			output := truncation.Content

			var details WebSearchToolDetails
			if provider != "" {
				details.Provider = provider
			}
			if truncation.Truncated {
				details.Truncation = &truncation
				output += fmt.Sprintf("\n\n[%s limit reached]", FormatSize(cfg.MaxBytes))
			}

			return result(output, details)
		},
	}
}

// coerceCountToInt converts a string "count" value to integer in JSON.
// Some LLMs pass numeric params as strings (e.g. "count": "10" not "count": 10).
func coerceCountToInt(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw // not valid JSON, let the caller handle it
	}
	if v, ok := m["count"]; ok {
		switch s := v.(type) {
		case string:
			if n, err := strconv.Atoi(s); err == nil {
				m["count"] = n
			}
		}
	}
	result, _ := json.Marshal(m)
	return result
}
