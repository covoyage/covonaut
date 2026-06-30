package tools

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

func searchDuckDuckGo(client *http.Client, query string, count int) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("q", query)
	req, err := http.NewRequest(http.MethodGet, "https://html.duckduckgo.com/html/?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultSearchUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Dnt", "1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("duckduckgo search returned HTTP %d", resp.StatusCode)
	}
	return parseDuckDuckGoHTML(resp.Body, count)
}

func parseDuckDuckGoHTML(r io.Reader, count int) ([]SearchResult, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, fmt.Errorf("parse duckduckgo html: %w", err)
	}

	var results []SearchResult

	// Primary selector: modern DuckDuckGo HTML
	doc.Find(".result").Each(func(i int, s *goquery.Selection) {
		if len(results) >= count {
			return
		}
		extractDuckDuckGoResult(s, &results)
	})

	// Fallback: older or alternative HTML structure
	if len(results) == 0 {
		doc.Find(".results_links").Each(func(i int, s *goquery.Selection) {
			if len(results) >= count {
				return
			}
			extractDuckDuckGoResult(s, &results)
		})
	}

	// Fallback: generic link extraction for any page
	if len(results) == 0 {
		doc.Find("a[href^='http']").Each(func(i int, s *goquery.Selection) {
			if len(results) >= count {
				return
			}
			href, exists := s.Attr("href")
			if !exists || href == "" {
				return
			}
			title := strings.TrimSpace(s.Text())
			if title == "" {
				return
			}
			results = append(results, SearchResult{Title: title, URL: href})
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("duckduckgo returned no parseable results")
	}
	return limitResults(results, count), nil
}

func extractDuckDuckGoResult(s *goquery.Selection, results *[]SearchResult) {
	// Try modern class names first
	link := s.Find(".result__a")
	href, exists := link.Attr("href")

	// Fallback: try title/link element
	if !exists || href == "" {
		link = s.Find(".result__title a, .result__url, .result__a")
		href, exists = link.Attr("href")
	}

	// Skip tracking/pixel URLs
	if strings.HasPrefix(href, "//duckduckgo.com/y.js") ||
		strings.HasPrefix(href, "//duckduckgo.com/l/") ||
		strings.HasPrefix(href, "javascript:") {
		return
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}

	title := strings.TrimSpace(link.Text())
	if title == "" {
		return
	}

	snippet := strings.TrimSpace(s.Find(".result__snippet").Text())
	if snippet == "" {
		snippet = strings.TrimSpace(s.Find(".result__snippet a, .snippet").Text())
	}

	*results = append(*results, SearchResult{Title: title, URL: href, Snippet: snippet})
}
