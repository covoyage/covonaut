package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"github.com/covoyage/covonaut/agentcore"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

var defaultUserAgents = []string{
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
}

var blockPagePatterns = []string{
	"Just a moment...",
	"cf-browser-verification",
	"checking your browser",
	"challenge-form",
	"Cloudflare",
	"_cf_chl_opt",
	"attention required",
	"enable javascript",
	"verify you are human",
	"are you a human",
	"captcha",
	"captcha-delivery",
	"access denied",
	"blocked",
	"please turn javascript on",
}

// WebFetchOperations defines pluggable operations for the web fetch tool.
type WebFetchOperations interface {
	Fetch(url string) (string, error)
}

// DefaultWebFetchOperations uses a browser-like HTTP client.
type DefaultWebFetchOperations struct {
	Client      *http.Client
	UserAgent   string
	MaxRetries  int
	RetryDelay  time.Duration
}

func (d *DefaultWebFetchOperations) defaults() {
	if d.Client == nil {
		d.Client = &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		}
	}
	if d.UserAgent == "" {
		d.UserAgent = defaultUserAgents[rand.Intn(len(defaultUserAgents))]
	}
	if d.MaxRetries <= 0 {
		d.MaxRetries = 2
	}
	if d.RetryDelay <= 0 {
		d.RetryDelay = 2 * time.Second
	}
}

func (d *DefaultWebFetchOperations) newRequest(url string) (*http.Request, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", d.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")
	// Note: DO NOT set Accept-Encoding manually.
	// Go's http.Transport auto-sets "gzip" and auto-decompresses when the header is absent.
	// Setting it manually disables auto-decompression, causing raw gzip bytes in the body.
	// req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="125", "Chromium";v="125", "Not.A/Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Dnt", "1")
	return req, nil
}

func (d *DefaultWebFetchOperations) Fetch(url string) (string, error) {
	d.defaults()

	var lastErr error
	for attempt := 0; attempt <= d.MaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(d.RetryDelay * time.Duration(attempt))
		}

		req, err := d.newRequest(url)
		if err != nil {
			return "", fmt.Errorf("create request: %w", err)
		}

		resp, err := d.Client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		// Detect and decode charset (GB2312/GBK/GB18030 → UTF-8 for Chinese .gov.cn sites)
		bodyReader, _ := charset.NewReader(resp.Body, resp.Header.Get("Content-Type"))
		body, readErr := io.ReadAll(io.LimitReader(bodyReader, 10*1024*1024))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}

		bodyStr := string(body)

		// Fallback: if body contains invalid UTF-8, try to detect charset from HTML meta tags
		if !utf8.Valid(body) {
			if decoded := tryDecodeNonUTF8(body); decoded != "" {
				bodyStr = decoded
			}
		}

		// Check for anti-bot / block pages regardless of status code
		isBlocked := isBlockPage(bodyStr)

		if resp.StatusCode == 403 || resp.StatusCode == 503 || resp.StatusCode == 429 || isBlocked {
			if isBlocked {
				lastErr = fmt.Errorf("BLOCKED: the target site returned an anti-bot / captcha / challenge page (HTTP %d). This site requires JavaScript rendering. Use browser (action=navigate) instead of web_fetch to access this URL", resp.StatusCode)
				continue
			}
			if resp.StatusCode == 429 {
				lastErr = fmt.Errorf("BLOCKED: rate limited (HTTP 429). Try again later or use browser (action=navigate) instead")
				continue
			}
			lastErr = fmt.Errorf("BLOCKED: HTTP %d. The target site may be blocking automated requests. Use browser (action=navigate) instead", resp.StatusCode)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("HTTP %d", resp.StatusCode)
		}

		return bodyStr, nil
	}

	return "", fmt.Errorf("fetch failed after %d retries: %w", d.MaxRetries, lastErr)
}

var metaCharsetRE = regexp.MustCompile(`(?i)<meta[^>]+charset\s*=\s*["']?\s*([a-zA-Z0-9_-]+)`)
var html5CharsetRE = regexp.MustCompile(`(?i)<meta\s+charset\s*=\s*["']?\s*([a-zA-Z0-9_-]+)`)

// tryDecodeNonUTF8 attempts to decode non-UTF-8 content by detecting charset
// from HTML meta tags, falling back to GB18030 for Chinese government sites.
func tryDecodeNonUTF8(raw []byte) string {
	bodyStr := string(raw)

	// Try to find charset from HTML meta tags
	for _, re := range []*regexp.Regexp{metaCharsetRE, html5CharsetRE} {
		matches := re.FindStringSubmatch(bodyStr)
		if len(matches) >= 2 {
			enc := strings.ToLower(matches[1])
			switch {
			case enc == "gb2312" || enc == "gbk" || enc == "gb18030" || strings.Contains(enc, "gb"):
				decoded, _, err := transform.String(simplifiedchinese.GB18030.NewDecoder(), bodyStr)
				if err == nil && utf8.ValidString(decoded) {
					return decoded
				}
			case enc == "big5" || enc == "big5-hkscs" || enc == "euc-kr":
				// Big5 and EUC-KR not supported without additional packages; skip
			}
		}
	}

	// Last resort: try GB18030 (superset of GB2312 and GBK, covers most Chinese gov sites)
	decoded, _, err := transform.String(simplifiedchinese.GB18030.NewDecoder(), bodyStr)
	if err == nil && utf8.ValidString(decoded) && len(decoded) > 0 {
		// Verify the decoded content actually has Chinese characters (not garbage)
		hasChinese := false
		for _, r := range decoded {
			if r > 0x4E00 && r < 0x9FFF {
				hasChinese = true
				break
			}
		}
		if hasChinese {
			return decoded
		}
	}

	return ""
}

func isBlockPage(body string) bool {
	lower := strings.ToLower(body)
	for _, pattern := range blockPagePatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// WebFetchToolConfig configures the web fetch tool.
type WebFetchToolConfig struct {
	Operations WebFetchOperations
	MaxBytes   int64
	MaxLines   int64
}

func (c *WebFetchToolConfig) defaults() {
	if c.Operations == nil {
		c.Operations = &DefaultWebFetchOperations{}
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = 50 * 1024
	}
	if c.MaxLines <= 0 {
		c.MaxLines = 2000
	}
}

// WebFetchToolInput is the JSON arguments for the web fetch tool.
type WebFetchToolInput struct {
	URL string `json:"url"`
}

// WebFetchToolDetails carries truncation metadata.
type WebFetchToolDetails struct {
	Truncation *TruncationResult `json:"truncation,omitempty"`
}

// NewWebFetchTool creates a web fetch tool.
func NewWebFetchTool(cfg *WebFetchToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &WebFetchToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "web_fetch",
		Description: fmt.Sprintf("Fetch and extract readable content from a URL (HTML → markdown/text). Output is truncated to %d lines or %s (whichever is hit first).", cfg.MaxLines, FormatSize(cfg.MaxBytes)),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "description": "HTTP or HTTPS URL to fetch"},
			},
			"required": []any{"url"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input WebFetchToolInput
			if err := json.Unmarshal(args, &input); err != nil {
				return resultErrf("invalid arguments: %w", err)
			}

			if input.URL == "" {
				return resultErrf("url is required")
			}

			if !strings.HasPrefix(input.URL, "http://") && !strings.HasPrefix(input.URL, "https://") {
				return resultErrf("invalid URL: must start with http:// or https://")
			}

			body, err := cfg.Operations.Fetch(input.URL)
			if err != nil {
				return resultErrf("fetch failed: %w", err)
			}

			// Simple HTML to text extraction.
			text := extractText(body)

			truncation := TruncateHead(text, TruncationOptions{
				MaxLines: int(cfg.MaxLines),
				MaxBytes: int(cfg.MaxBytes),
			})

			output := truncation.Content
			if truncation.Truncated {
				notices := []string{}
				if truncation.TruncatedBy == "lines" {
					notices = append(notices, fmt.Sprintf("%d lines limit reached", cfg.MaxLines))
				} else {
					notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(cfg.MaxBytes)))
				}
				output += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
			}

			return result(output, WebFetchToolDetails{Truncation: &truncation})
		},
	}
}

// extractText performs HTML to text extraction using goquery.
func extractText(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return html
	}

	// Remove non-content elements.
	for _, sel := range []string{"script", "style", "noscript", "nav", "footer", "header", "aside", ".sidebar", "#sidebar", ".nav", ".menu", ".footer", ".header", "svg", "form", "iframe"} {
		doc.Find(sel).Remove()
	}

	var parts []string
	doc.Find("body").First().Children().Each(func(i int, s *goquery.Selection) {
		parts = append(parts, extractNodeText(s, 0))
	})

	text := strings.Join(parts, "\n")

	// Clean up excessive whitespace.
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}

	return strings.Join(cleaned, "\n")
}

func extractNodeText(s *goquery.Selection, depth int) string {
	if depth > 20 {
		return ""
	}

	tag := goquery.NodeName(s)
	text := strings.TrimSpace(s.Text())

	// Handle links: show [text](url)
	if tag == "a" {
		if href, ok := s.Attr("href"); ok && href != "" && !strings.HasPrefix(href, "#") && !strings.HasPrefix(href, "javascript:") {
			if text != "" {
				return fmt.Sprintf("[%s](%s)", text, href)
			}
		}
		return text
	}

	// Handle images
	if tag == "img" {
		alt, _ := s.Attr("alt")
		src, _ := s.Attr("src")
		if alt != "" {
			return fmt.Sprintf("[image: %s]", alt)
		} else if src != "" {
			return fmt.Sprintf("[image: %s]", src)
		}
		return ""
	}

	// For block-level elements, recurse into children with newline separators
	if isBlockElement(tag) {
		var children []string
		s.Children().Each(func(i int, child *goquery.Selection) {
			childText := extractNodeText(child, depth+1)
			if childText != "" {
				children = append(children, childText)
			}
		})
		if len(children) == 0 {
			if text != "" {
				if tag == "li" {
					return "- " + text
				}
				if tag == "td" || tag == "th" {
					return text + "\t"
				}
				if tag == "br" || tag == "hr" {
					return ""
				}
				return text
			}
			return ""
		}
		if tag == "li" {
			return "- " + strings.Join(children, " ")
		}
		return strings.Join(children, "\n")
	}

	// Inline elements: just return text
	if text == "" {
		return ""
	}
	return text
}

func isBlockElement(tag string) bool {
	switch tag {
	case "p", "div", "h1", "h2", "h3", "h4", "h5", "h6",
		"ul", "ol", "li", "table", "tr", "td", "th",
		"section", "article", "main", "blockquote", "pre",
		"br", "hr", "dl", "dt", "dd":
		return true
	}
	return false
}
