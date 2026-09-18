package tools

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolveSearchProviderOrder_AutoKeyless(t *testing.T) {
	t.Setenv("WEB_SEARCH_PROVIDER", "")
	t.Setenv("WEB_SEARCH_REGION", "us")
	t.Setenv("SERPAPI_API_KEY", "")
	t.Setenv("BRAVE_SEARCH_API_KEY", "")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("SEARXNG_URL", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("WEB_SEARCH_API_URL", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("API_KEY", "")

	got := resolveSearchProviderOrder(nil)
	want := []searchProviderID{providerDuckDuckGo, providerBing}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestResolveSearchProviderOrder_AutoKeylessChina(t *testing.T) {
	t.Setenv("WEB_SEARCH_PROVIDER", "")
	t.Setenv("WEB_SEARCH_REGION", "cn")
	t.Setenv("SERPAPI_API_KEY", "")
	t.Setenv("BRAVE_SEARCH_API_KEY", "")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("SEARXNG_URL", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("WEB_SEARCH_API_URL", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("API_KEY", "")

	got := resolveSearchProviderOrder(nil)
	want := []searchProviderID{providerBing, providerDuckDuckGo}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestRegionFromLocale(t *testing.T) {
	tests := map[string]string{
		"zh_CN.UTF-8": "cn",
		"zh-Hans":     "cn",
		"en_US.UTF-8": "us",
		"ja_JP":       "jp",
		"C":           "",
		"":            "",
	}
	for in, want := range tests {
		if got := regionFromLocale(in); got != want {
			t.Errorf("regionFromLocale(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestResolveSearchProviderOrder_CredentialedFirst(t *testing.T) {
	t.Setenv("WEB_SEARCH_PROVIDER", "")
	t.Setenv("WEB_SEARCH_REGION", "us")
	t.Setenv("SERPAPI_API_KEY", "test")
	t.Setenv("BRAVE_SEARCH_API_KEY", "test")
	t.Setenv("TAVILY_API_KEY", "")
	t.Setenv("SEARXNG_URL", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("WEB_SEARCH_API_URL", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("API_KEY", "")

	got := resolveSearchProviderOrder(nil)
	want := []searchProviderID{providerSerpAPI, providerBrave, providerDuckDuckGo, providerBing}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestResolveSearchProviderOrder_Explicit(t *testing.T) {
	cfg := &WebSearchToolConfig{Provider: "duckduckgo"}
	got := resolveSearchProviderOrder(cfg)
	want := []searchProviderID{providerDuckDuckGo}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestParseDuckDuckGoHTML(t *testing.T) {
	html := `<html><body>
<div class="result">
  <a class="result__a" href="https://example.com/a">Title A</a>
  <a class="result__snippet">Snippet A</a>
</div>
<div class="result">
  <a class="result__a" href="https://example.com/b">Title B</a>
  <a class="result__snippet">Snippet B</a>
</div>
</body></html>`

	results, err := parseDuckDuckGoHTML(strings.NewReader(html), 10)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Title A" || results[0].URL != "https://example.com/a" || results[0].Snippet != "Snippet A" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
}

func TestReadWebSearchEnv_InjectsGeminiKey(t *testing.T) {
	cfg := &WebSearchToolConfig{
		GeminiAPIKey:     "injected",
		ChatProviderName: "gemini",
		Provider:         "auto",
	}
	env := readWebSearchEnv(cfg)
	if env.GeminiKey != "injected" {
		t.Fatalf("expected injected gemini key, got %q", env.GeminiKey)
	}
	if env.ChatProvider != "gemini" {
		t.Fatalf("expected chat provider gemini, got %q", env.ChatProvider)
	}
}
