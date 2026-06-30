package tools

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// defaultSearchUserAgent is the User-Agent for search engine HTTP requests.
// Using a real Chrome UA reduces the chance of being blocked compared to a bot identifier.
const defaultSearchUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

type searchProviderID string

const (
	providerSerpAPI    searchProviderID = "serpapi"
	providerBrave      searchProviderID = "brave"
	providerTavily     searchProviderID = "tavily"
	providerSearXNG    searchProviderID = "searxng"
	providerGemini     searchProviderID = "gemini"
	providerGeneric    searchProviderID = "generic"
	providerDuckDuckGo searchProviderID = "duckduckgo"
	providerBing       searchProviderID = "bing"
)

type webSearchEnv struct {
	ExplicitProvider string
	APIURL           string
	APIKey           string
	SerpAPIKey       string
	BraveKey         string
	TavilyKey        string
	SearXNGURL       string
	GeminiKey        string
	InjectedGemini   string
	ChatProvider     string
}

func readWebSearchEnv(cfg *WebSearchToolConfig) webSearchEnv {
	env := webSearchEnv{
		ExplicitProvider: strings.ToLower(strings.TrimSpace(os.Getenv("WEB_SEARCH_PROVIDER"))),
		APIURL:           strings.TrimSpace(os.Getenv("WEB_SEARCH_API_URL")),
		APIKey:           strings.TrimSpace(os.Getenv("WEB_SEARCH_API_KEY")),
		SerpAPIKey:       strings.TrimSpace(os.Getenv("SERPAPI_API_KEY")),
		BraveKey:         strings.TrimSpace(os.Getenv("BRAVE_SEARCH_API_KEY")),
		TavilyKey:        strings.TrimSpace(os.Getenv("TAVILY_API_KEY")),
		SearXNGURL:       strings.TrimSpace(os.Getenv("SEARXNG_URL")),
	}
	if cfg != nil {
		if p := strings.ToLower(strings.TrimSpace(cfg.Provider)); p != "" {
			env.ExplicitProvider = p
		}
		env.InjectedGemini = strings.TrimSpace(cfg.GeminiAPIKey)
		env.ChatProvider = strings.ToLower(strings.TrimSpace(cfg.ChatProviderName))
	}
	env.GeminiKey = firstNonEmpty(env.InjectedGemini, os.Getenv("GEMINI_API_KEY"), os.Getenv("GOOGLE_API_KEY"), os.Getenv("API_KEY"))
	return env
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func resolveSearchProviderOrder(cfg *WebSearchToolConfig) []searchProviderID {
	env := readWebSearchEnv(cfg)
	if env.ExplicitProvider != "" && env.ExplicitProvider != "auto" {
		return []searchProviderID{searchProviderID(env.ExplicitProvider)}
	}

	var order []searchProviderID
	if env.SerpAPIKey != "" {
		order = append(order, providerSerpAPI)
	}
	if env.BraveKey != "" {
		order = append(order, providerBrave)
	}
	if env.TavilyKey != "" {
		order = append(order, providerTavily)
	}
	if env.SearXNGURL != "" {
		order = append(order, providerSearXNG)
	}
	if env.GeminiKey != "" {
		order = append(order, providerGemini)
	}
	if env.APIURL != "" {
		order = append(order, providerGeneric)
	}
	order = append(order, providerDuckDuckGo, providerBing)
	return order
}

func searchWithProvider(client *http.Client, provider searchProviderID, query string, count int, cfg *WebSearchToolConfig) ([]SearchResult, error) {
	env := readWebSearchEnv(cfg)
	switch provider {
	case providerSerpAPI:
		return searchSerpAPI(client, query, count, env.SerpAPIKey)
	case providerBrave:
		return searchBrave(client, query, count, env.BraveKey)
	case providerTavily:
		return searchTavily(client, query, count, env.TavilyKey)
	case providerSearXNG:
		return searchSearXNG(client, query, count, env.SearXNGURL)
	case providerGemini:
		return searchGeminiGrounding(client, query, count, env.GeminiKey, cfg)
	case providerGeneric:
		apiURL := env.APIURL
		apiKey := env.APIKey
		if cfg != nil && cfg.APIURL != "" {
			apiURL = cfg.APIURL
			apiKey = cfg.APIKey
		}
		return searchGenericJSON(client, apiURL, query, count, apiKey)
	case providerDuckDuckGo:
		return searchDuckDuckGo(client, query, count)
	case providerBing:
		return searchBingRSS(client, query, count)
	default:
		return nil, fmt.Errorf("unknown search provider %q", provider)
	}
}

func searchWithFallbackChain(client *http.Client, query string, count int, cfg *WebSearchToolConfig) (string, []SearchResult, error) {
	order := resolveSearchProviderOrder(cfg)
	var errors []string
	for _, provider := range order {
		results, err := searchWithProvider(client, provider, query, count, cfg)
		if err == nil && len(results) > 0 {
			return string(provider), results, nil
		}
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", provider, err))
		} else {
			errors = append(errors, fmt.Sprintf("%s: no results", provider))
		}
	}
	return "", nil, fmt.Errorf("all search providers failed: %s", strings.Join(errors, "; "))
}

func newSearchHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second}
}
