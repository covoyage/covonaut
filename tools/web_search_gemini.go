package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func searchGeminiGrounding(client *http.Client, query string, count int, apiKey string, cfg *WebSearchToolConfig) ([]SearchResult, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("gemini API key not configured")
	}
	baseURL := envOrDefault("GEMINI_BASE_URL", "https://generativelanguage.googleapis.com/v1beta")
	model := envOrDefault("GEMINI_SEARCH_MODEL", "gemini-2.0-flash")
	if cfg != nil && strings.TrimSpace(cfg.GeminiModel) != "" {
		model = strings.TrimSpace(cfg.GeminiModel)
	}

	endpoint := fmt.Sprintf("%s/models/%s:generateContent", strings.TrimRight(baseURL, "/"), model)
	body := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]any{{"text": query}}},
		},
		"tools": []map[string]any{
			{"google_search": map[string]any{}},
		},
	}
	payload, _ := json.Marshal(body)

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			GroundingMetadata struct {
				GroundingChunks []struct {
					Web struct {
						URI   string `json:"uri"`
						Title string `json:"title"`
					} `json:"web"`
				} `json:"groundingChunks"`
			} `json:"groundingMetadata"`
		} `json:"candidates"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := doJSONRequest(client, req, &resp); err != nil {
		return nil, err
	}
	if resp.Error.Message != "" {
		return nil, fmt.Errorf("gemini search error: %s", resp.Error.Message)
	}

	results := make([]SearchResult, 0, count)
	seen := make(map[string]struct{})
	for _, candidate := range resp.Candidates {
		for _, chunk := range candidate.GroundingMetadata.GroundingChunks {
			url := strings.TrimSpace(chunk.Web.URI)
			if url == "" {
				continue
			}
			if _, ok := seen[url]; ok {
				continue
			}
			seen[url] = struct{}{}
			title := strings.TrimSpace(chunk.Web.Title)
			if title == "" {
				title = url
			}
			snippet := ""
			if len(candidate.Content.Parts) > 0 {
				snippet = cleanSearchText(candidate.Content.Parts[0].Text)
			}
			results = append(results, SearchResult{Title: title, URL: url, Snippet: snippet})
			if len(results) >= count {
				return results, nil
			}
		}
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("gemini returned no grounded web results")
	}
	return results, nil
}
