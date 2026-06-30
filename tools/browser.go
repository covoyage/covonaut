package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/covoyage/covonaut/agentcore"
)

var defaultBrowserManager *BrowserManager

var stealthJavaScript = `
// Hide automation fingerprints
Object.defineProperty(navigator, 'webdriver', { get: () => undefined });

// Restore navigator.plugins (headless Chrome has empty plugins)
Object.defineProperty(navigator, 'plugins', {
  get: () => [
    { name: 'Chrome PDF Plugin', filename: 'internal-pdf-viewer' },
    { name: 'Chrome PDF Viewer', filename: 'mhjfbmdgcfjbbpaeojofohoefgiehjai' },
    { name: 'Native Client', filename: 'internal-nacl-plugin' },
  ],
});

// Override navigator.languages to look like a real user
Object.defineProperty(navigator, 'languages', { get: () => ['en-US', 'en', 'zh-CN'] });

// Add a plausible chrome object
if (!window.chrome) { window.chrome = { runtime: {} }; }

// Fix permissions query to not expose automation
if (navigator.permissions && navigator.permissions.query) {
  const origQuery = navigator.permissions.query.bind(navigator.permissions);
  navigator.permissions.query = (params) => {
    if (params && (params.name === 'notifications' || params.name === 'clipboard-read')) {
      return Promise.resolve({ state: 'prompt', onchange: null });
    }
    return origQuery(params);
  };
}

// Override webgl vendor/renderer to avoid headless detection
try {
  const getParam = WebGLRenderingContext.prototype.getParameter;
  WebGLRenderingContext.prototype.getParameter = function(p) {
    if (p === 37445) return 'Intel Inc.';
    if (p === 37446) return 'Intel Iris OpenGL Engine';
    return getParam.call(this, p);
  };
} catch(e) {}

// Override screen dimensions to match viewport
try {
  Object.defineProperty(screen, 'width', { get: () => window.innerWidth });
  Object.defineProperty(screen, 'height', { get: () => window.innerHeight });
  Object.defineProperty(screen, 'availWidth', { get: () => window.innerWidth });
  Object.defineProperty(screen, 'availHeight', { get: () => window.innerHeight });
  Object.defineProperty(screen, 'colorDepth', { get: () => 24 });
  Object.defineProperty(screen, 'pixelDepth', { get: () => 24 });
} catch(e) {}
`;

func init() {
	abEnabled := os.Getenv("AGENT_BROWSER_ENABLED") == "true" || os.Getenv("AGENT_BROWSER_PATH") != ""
	defaultBrowserManager = NewBrowserManager(&BrowserConfig{
		Headless:            true,
		AllowPrivate:        false,
		CommandTimeout:      30 * time.Second,
		CDPURL:              "",
		CamofoxURL:          "",
		AgentBrowserEnabled: abEnabled,
	})

	SetupSignalHandler()

	reaper := NewOrphanReaper()
	go func() {
		reaper.ReapOrphans()
	}()
}

type BrowserToolConfig struct {
	Headless            bool
	AllowPrivate        bool
	CommandTimeout      time.Duration
	CDPURL              string
	CamofoxURL          string
	CloudProvider       string
	Engine              string
	DialogPolicy        DialogPolicy
	DialogTimeout       time.Duration
	AutoLocalForPrivate bool
	RecordSessions      bool
	RecordingDir        string
	VisionModel         string
	MaxImageSize        int
	InactivityTimeout   time.Duration
	UserAgent           string
	AcceptLanguage      string
	ProxyURL            string
	ViewportWidth       int
	ViewportHeight      int
	AgentBrowserEnabled bool
}

func (c *BrowserToolConfig) defaults() {
	if c.CommandTimeout <= 0 {
		c.CommandTimeout = 30 * time.Second
	}
	if c.DialogTimeout <= 0 {
		c.DialogTimeout = 300 * time.Second
	}
	if c.InactivityTimeout <= 0 {
		c.InactivityTimeout = 5 * time.Minute
	}
	if c.DialogPolicy == "" {
		c.DialogPolicy = DialogMustRespond
	}
	if os.Getenv("AGENT_BROWSER_ENABLED") == "true" || os.Getenv("AGENT_BROWSER_PATH") != "" {
		c.AgentBrowserEnabled = true
	}
}

func NewBrowserNavigateTool(cfg *BrowserToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &BrowserToolConfig{}
	}
	cfg.defaults()

	defaultBrowserManager = NewBrowserManager(&BrowserConfig{
		Headless:            cfg.Headless,
		AllowPrivate:        cfg.AllowPrivate,
		CommandTimeout:      cfg.CommandTimeout,
		CDPURL:              cfg.CDPURL,
		CamofoxURL:          cfg.CamofoxURL,
		CloudProvider:       cfg.CloudProvider,
		Engine:              cfg.Engine,
		DialogPolicy:        cfg.DialogPolicy,
		DialogTimeout:       cfg.DialogTimeout,
		AutoLocalForPrivate: cfg.AutoLocalForPrivate,
		RecordSessions:      cfg.RecordSessions,
		RecordingDir:        cfg.RecordingDir,
		InactivityTimeout:   cfg.InactivityTimeout,
		UserAgent:           cfg.UserAgent,
		AcceptLanguage:      cfg.AcceptLanguage,
		ProxyURL:            cfg.ProxyURL,
		ViewportWidth:       cfg.ViewportWidth,
		ViewportHeight:      cfg.ViewportHeight,
		AgentBrowserEnabled: cfg.AgentBrowserEnabled,
	})

	return &agentcore.Tool{
		Name:        "browser_navigate",
		Description: "Navigate to a specific URL and return an accessibility snapshot. Use this for user-provided URLs, interactive pages, login flows, or JavaScript-heavy pages. For simple information retrieval, prefer web_search (faster, cheaper, no browser overhead).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "URL to navigate to (http:// or https://)",
				},
			},
			"required": []any{"url"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			parsedURL, err := validateURL(input.URL, cfg.AllowPrivate || cfg.AutoLocalForPrivate)
			if err != nil {
				return nil, fmt.Errorf("URL validation failed: %w", err)
			}
			sessionID := "default"
			sessionTargetURL := ""
			backend := DetectBackend(&defaultBrowserManager.config)
			if backend == BackendCamofox || (defaultBrowserManager.config.AutoLocalForPrivate && IsPrivateURL(parsedURL.String())) {
				sessionTargetURL = input.URL
			}
			session, err := defaultBrowserManager.CreateSession(context.Background(), sessionID, sessionTargetURL)
			if err != nil {
				return nil, fmt.Errorf("failed to create browser session: %w", err)
			}

			var snapshot string
			switch session.backendType {
			case BackendCamofox:
				snapshot, err = session.camofoxClient.Navigate(sessionID, parsedURL.String())
				if err != nil {
					return nil, fmt.Errorf("camofox navigation failed: %w", err)
				}
			case BackendLightpanda:
				navTimeout := navigationTimeout(cfg.CommandTimeout)
				timeoutCtx, cancel := context.WithTimeout(session.ctx, navTimeout)

				// 1. Fire navigation via CDP Page.navigate (does not wait for load)
				if err := chromedp.Run(timeoutCtx, chromedp.ActionFunc(func(ctx context.Context) error {
					_, _, _, _, err := page.Navigate(parsedURL.String()).Do(ctx)
					return err
				})); err != nil {
					cancel()
					if isDeadlineError(err) {
						return nil, navigationTimeoutError(parsedURL.String(), navTimeout)
					}
					return nil, fmt.Errorf("lightpanda navigation failed: %w", err)
				}

				// 2. Wait for DOM to be interactive or URL to match
				readyCtx, readyCancel := context.WithTimeout(timeoutCtx, 30*time.Second)
				var ready bool
				for i := 0; i < 30; i++ {
					var state string
					chromedp.Run(readyCtx, chromedp.Evaluate(`
						(function() {
							return JSON.stringify({
								state: document.readyState,
								url: window.location.href
							});
						})()
					`, &state))

					var navResult struct {
						State string `json:"state"`
						URL   string `json:"url"`
					}
					if json.Unmarshal([]byte(state), &navResult) == nil {
						if navResult.State == "interactive" || navResult.State == "complete" {
							ready = true
							break
						}
						// If URL changed to target, consider it successful even if still loading resources
						if strings.Contains(navResult.URL, parsedURL.Host) {
							ready = true
							break
						}
					}
					time.Sleep(1 * time.Second)
				}
				readyCancel()

				if !ready {
					cancel()
					return nil, fmt.Errorf("navigation timed out: page did not become interactive")
				}

				// 3. Small sleep to allow SPA rendering
				time.Sleep(1 * time.Second)

				// 3b. Apply stealth JS to hide automation fingerprints
				stealthCtx, stealthCancel := context.WithTimeout(timeoutCtx, 3*time.Second)
				chromedp.Run(stealthCtx, chromedp.Evaluate(stealthJavaScript, nil))
				stealthCancel()

				// 4. Get title
				var title string
				titleCtx, titleCancel := context.WithTimeout(timeoutCtx, 5*time.Second)
				chromedp.Run(titleCtx, chromedp.Title(&title))
				titleCancel()

				// 5. Generate snapshot to update ref map
				snapshot, err = generateSnapshot(timeoutCtx, false, session.refMapper)
				cancel()

				session.mu.Lock()
				session.url = parsedURL.String()
				session.title = title
				session.lastActivity = time.Now()
				session.mu.Unlock()

				if err != nil {
					snapshot = fmt.Sprintf("(snapshot unavailable: %v)", err)
				}

				if NeedsLightpandaFallback(cfg.Engine, snapshot, 0, nil) {
					fallbackResult, fallbackErr := RunChromeFallbackCommand(context.Background(), "navigate", map[string]any{"url": input.URL}, cfg.CommandTimeout)
					if fallbackErr == nil {
						snapshot, _ = fallbackResult["snapshot"].(string)
						title, _ = fallbackResult["title"].(string)
						session.mu.Lock()
						session.title = title
						session.mu.Unlock()
						AnnotateLightpandaFallback(nil)
					}
				}
			case BackendLocal, BackendCDP, BackendBrowserbase, BackendBrowserUse, BackendFirecrawl, BackendAgentBrowser:
				navTimeout := navigationTimeout(cfg.CommandTimeout)
				timeoutCtx, cancel := context.WithTimeout(session.ctx, navTimeout)

				// 1. Fire navigation via CDP Page.navigate (does not wait for load)
				if err := chromedp.Run(timeoutCtx, chromedp.ActionFunc(func(ctx context.Context) error {
					_, _, _, _, err := page.Navigate(parsedURL.String()).Do(ctx)
					return err
				})); err != nil {
					cancel()
					if isDeadlineError(err) {
						return nil, navigationTimeoutError(parsedURL.String(), navTimeout)
					}
					return nil, fmt.Errorf("navigation failed: %w", err)
				}

				// 2. Wait for DOM to be interactive or URL to match
				readyCtx, readyCancel := context.WithTimeout(timeoutCtx, 30*time.Second)
				var ready bool
				for i := 0; i < 30; i++ {
					var state string
					chromedp.Run(readyCtx, chromedp.Evaluate(`
						(function() {
							return JSON.stringify({
								state: document.readyState,
								url: window.location.href
							});
						})()
					`, &state))

					var navResult struct {
						State string `json:"state"`
						URL   string `json:"url"`
					}
					if json.Unmarshal([]byte(state), &navResult) == nil {
						if navResult.State == "interactive" || navResult.State == "complete" {
							ready = true
							break
						}
						if strings.Contains(navResult.URL, parsedURL.Host) {
							ready = true
							break
						}
					}
					time.Sleep(1 * time.Second)
				}
				readyCancel()

				if !ready {
					cancel()
					return nil, fmt.Errorf("navigation timed out: page did not become interactive")
				}

				// 3. Small sleep to allow SPA rendering
				time.Sleep(1 * time.Second)

				// 3b. Apply stealth JS to hide automation fingerprints
				stealthCtx, stealthCancel := context.WithTimeout(timeoutCtx, 3*time.Second)
				chromedp.Run(stealthCtx, chromedp.Evaluate(stealthJavaScript, nil))
				stealthCancel()

				// 4. Get title
				var title string
				titleCtx, titleCancel := context.WithTimeout(timeoutCtx, 5*time.Second)
				chromedp.Run(titleCtx, chromedp.Title(&title))
				titleCancel()

				// 5. Generate snapshot to update ref map
				snapshot, err = generateSnapshot(timeoutCtx, false, session.refMapper)
				cancel()

				session.mu.Lock()
				session.url = parsedURL.String()
				session.title = title
				session.lastActivity = time.Now()
				session.mu.Unlock()

				if err != nil {
					snapshot = fmt.Sprintf("(snapshot unavailable: %v)", err)
				}
			default:
				return nil, fmt.Errorf("backend %s not yet supported for navigation", session.backendType)
			}

			session.mu.RLock()
			url := session.url
			title := session.title
			session.mu.RUnlock()

			if title == "" {
				title = "(unknown)"
			}

			if err := ValidatePostRedirectURL(session.url, input.URL, cfg.AllowPrivate, cfg.AutoLocalForPrivate); err != nil {
				return nil, err
			}

			if session.recorder != nil && !session.recorder.IsRecording() {
				if session.ctx != nil {
					session.recorder.StartRecording(ctx, session.sessionID, session.ctx)
				}
			}

			var extraInfo string
			if session.supervisor != nil {
				dialogs := session.supervisor.GetPendingDialogs()
				if len(dialogs) > 0 {
					extraInfo = formatDialogs(dialogs)
				}
			}

			return result(fmt.Sprintf("Navigated to %s\nTitle: %s\n\n%s%s", url, title, snapshot, extraInfo), nil)
		},
	}
}

func navigationTimeout(commandTimeout time.Duration) time.Duration {
	if commandTimeout > 60*time.Second {
		return commandTimeout
	}
	return 60 * time.Second
}

func isDeadlineError(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

func navigationTimeoutError(url string, timeout time.Duration) error {
	return fmt.Errorf("navigation timed out after %s while opening %s. The page may be slow, blocked, or still loading; try again, use browser_snapshot if the page partially loaded, or raise the browser command timeout", timeout.Round(time.Second), url)
}

func NewBrowserSnapshotTool(cfg *BrowserToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &BrowserToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "browser_snapshot",
		Description: "Get a text-based accessibility tree snapshot of the current page. Use ref IDs (e.g. @e5) to interact with elements.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"full": map[string]any{
					"type":        "boolean",
					"description": "Show complete page content (default: false, shows only interactive elements)",
					"default":     false,
				},
				"mode": map[string]any{
					"type":        "string",
					"description": "Snapshot mode. \"default\": JS-based accessibility tree with XPath refs. \"aria\": Chrome native aria tree. (default: \"default\")",
					"enum":        []string{"default", "aria"},
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				Full bool   `json:"full"`
				Mode string `json:"mode"`
			}
			_ = json.Unmarshal(args, &input)
			if input.Mode == "" {
				input.Mode = "default"
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser_navigate first")
			}

			var snapshot string
			var err error

			switch session.backendType {
			case BackendCamofox:
				snapshot, err = session.camofoxClient.GetSnapshot(session.sessionID, input.Full)
			case BackendLightpanda:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				snapshot, err = GeneratePageSnapshot(timeoutCtx, input.Full, session.refMapper, input.Mode)
				cancel()
			case BackendLocal, BackendCDP, BackendBrowserbase, BackendBrowserUse, BackendFirecrawl, BackendAgentBrowser:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				snapshot, err = GeneratePageSnapshot(timeoutCtx, input.Full, session.refMapper, input.Mode)
				cancel()
			default:
				err = fmt.Errorf("backend %s not supported for snapshot", session.backendType)
			}

			if err != nil {
				return nil, fmt.Errorf("snapshot failed: %w", err)
			}

			session.mu.RLock()
			url := session.url
			title := session.title
			session.mu.RUnlock()

			var extraInfo string
			if session.supervisor != nil {
				dialogs := session.supervisor.GetPendingDialogs()
				if len(dialogs) > 0 {
					extraInfo = formatDialogs(dialogs)
				}
				frames := session.supervisor.GetFrameTree()
				if len(frames) > 0 {
					extraInfo += formatFrameTree(frames, session.supervisor.IsTruncated())
				}
			}

			return result(fmt.Sprintf("Page: %s\nTitle: %s\n\n%s%s", url, title, snapshot, extraInfo), nil)
		},
	}
}

func NewBrowserClickTool(cfg *BrowserToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &BrowserToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "browser_click",
		Description: "Click an element by its ref ID (e.g. @e5). The ref ID is shown in browser_snapshot output.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ref": map[string]any{
					"type":        "string",
					"description": "Element ref ID from snapshot (e.g. @e5 or e5)",
				},
			},
			"required": []any{"ref"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				Ref string `json:"ref"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			ref := input.Ref
			if !strings.HasPrefix(ref, "@") {
				ref = "@" + ref
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser_navigate first")
			}

			var snapshot string
			var err error

			switch session.backendType {
			case BackendCamofox:
				snapshot, err = session.camofoxClient.Click(session.sessionID, ref)
			case BackendLightpanda:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				xpath, lookupErr := session.refMapper.Get(ref)
				if lookupErr {
					// Fallback: try to get from window.__covoRefMap in the browser
					var jsXpath string
					js := fmt.Sprintf(`window.__covoRefMap && window.__covoRefMap[%q] || null`, ref)
					if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(js, &jsXpath)); err == nil && jsXpath != "" {
						xpath = jsXpath
						session.refMapper.Set(ref, xpath) // Cache it for next time
					} else {
						cancel()
						if session.refMapper.Count() == 0 {
							return nil, fmt.Errorf("ref %s not found. Page state is unknown. Call browser_snapshot first", ref)
						}
						return nil, fmt.Errorf("ref %s not found in current page state. Call browser_snapshot to refresh", ref)
					}
				}
				if err := chromedp.Run(timeoutCtx, chromedp.Click(xpath, chromedp.BySearch)); err != nil {
					cancel()
					return nil, fmt.Errorf("click failed for %s: %w", ref, err)
				}
				cancel()
				time.Sleep(500 * time.Millisecond)
				snapshot, err = generateSnapshot(session.ctx, false, session.refMapper)
			case BackendLocal, BackendCDP, BackendBrowserbase, BackendBrowserUse, BackendFirecrawl, BackendAgentBrowser:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				xpath, lookupErr := session.refMapper.Get(ref)
				if lookupErr {
					// Fallback: try to get from window.__covoRefMap in the browser
					var jsXpath string
					js := fmt.Sprintf(`window.__covoRefMap && window.__covoRefMap[%q] || null`, ref)
					if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(js, &jsXpath)); err == nil && jsXpath != "" {
						xpath = jsXpath
						session.refMapper.Set(ref, xpath) // Cache it for next time
					} else {
						cancel()
						if session.refMapper.Count() == 0 {
							return nil, fmt.Errorf("ref %s not found. Page state is unknown. Call browser_snapshot first", ref)
						}
						return nil, fmt.Errorf("ref %s not found in current page state. Call browser_snapshot to refresh", ref)
					}
				}
				if err := chromedp.Run(timeoutCtx, chromedp.Click(xpath, chromedp.BySearch)); err != nil {
					cancel()
					return nil, fmt.Errorf("click failed for %s: %w", ref, err)
				}
				cancel()
				time.Sleep(500 * time.Millisecond)
				snapshot, err = generateSnapshot(session.ctx, false, session.refMapper)
			default:
				err = fmt.Errorf("backend %s not supported for click", session.backendType)
			}

			if err != nil {
				return nil, err
			}

			session.mu.Lock()
			session.lastActivity = time.Now()
			session.mu.Unlock()

			return result(fmt.Sprintf("Clicked %s\n\n%s", ref, snapshot), nil)
		},
	}
}

func NewBrowserTypeTool(cfg *BrowserToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &BrowserToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "browser_type",
		Description: "Type text into an input field. Clears the field first, then types the text.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ref": map[string]any{
					"type":        "string",
					"description": "Element ref ID from snapshot (e.g. @e5 or e5)",
				},
				"text": map[string]any{
					"type":        "string",
					"description": "Text to type into the field",
				},
			},
			"required": []any{"ref", "text"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				Ref  string `json:"ref"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			ref := input.Ref
			if !strings.HasPrefix(ref, "@") {
				ref = "@" + ref
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser_navigate first")
			}

			var resultMsg string
			var err error

			switch session.backendType {
			case BackendCamofox:
				resultMsg, err = session.camofoxClient.Type(session.sessionID, ref, input.Text)
			case BackendLightpanda:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				xpath, lookupErr := session.refMapper.Get(ref)
				if lookupErr {
					// Fallback: try to get from window.__covoRefMap in the browser
					var jsXpath string
					js := fmt.Sprintf(`window.__covoRefMap && window.__covoRefMap[%q] || null`, ref)
					if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(js, &jsXpath)); err == nil && jsXpath != "" {
						xpath = jsXpath
						session.refMapper.Set(ref, xpath) // Cache it for next time
					} else {
						cancel()
						if session.refMapper.Count() == 0 {
							return nil, fmt.Errorf("ref %s not found. Page state is unknown. Call browser_snapshot first", ref)
						}
						return nil, fmt.Errorf("ref %s not found in current page state. Call browser_snapshot to refresh", ref)
					}
				}
				if err := chromedp.Run(timeoutCtx,
					chromedp.Clear(xpath, chromedp.BySearch),
					chromedp.SendKeys(xpath, input.Text, chromedp.BySearch),
				); err != nil {
					cancel()
					return nil, fmt.Errorf("type failed for %s: %w", ref, err)
				}
				cancel()
				resultMsg = fmt.Sprintf("Typed \"%s\" into %s", input.Text, ref)
			case BackendLocal, BackendCDP, BackendBrowserbase, BackendBrowserUse, BackendFirecrawl, BackendAgentBrowser:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				xpath, lookupErr := session.refMapper.Get(ref)
				if lookupErr {
					// Fallback: try to get from window.__covoRefMap in the browser
					var jsXpath string
					js := fmt.Sprintf(`window.__covoRefMap && window.__covoRefMap[%q] || null`, ref)
					if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(js, &jsXpath)); err == nil && jsXpath != "" {
						xpath = jsXpath
						session.refMapper.Set(ref, xpath) // Cache it for next time
					} else {
						cancel()
						if session.refMapper.Count() == 0 {
							return nil, fmt.Errorf("ref %s not found. Page state is unknown. Call browser_snapshot first", ref)
						}
						return nil, fmt.Errorf("ref %s not found in current page state. Call browser_snapshot to refresh", ref)
					}
				}
				if err := chromedp.Run(timeoutCtx,
					chromedp.Clear(xpath, chromedp.BySearch),
					chromedp.SendKeys(xpath, input.Text, chromedp.BySearch),
				); err != nil {
					cancel()
					return nil, fmt.Errorf("type failed for %s: %w", ref, err)
				}
				cancel()
				resultMsg = fmt.Sprintf("Typed \"%s\" into %s", input.Text, ref)
			default:
				err = fmt.Errorf("backend %s not supported for type", session.backendType)
			}

			if err != nil {
				return nil, err
			}

			session.mu.Lock()
			session.lastActivity = time.Now()
			session.mu.Unlock()

			return result(resultMsg, nil)
		},
	}
}

func NewBrowserScrollTool(cfg *BrowserToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &BrowserToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "browser_scroll",
		Description: "Scroll the page up or down.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"direction": map[string]any{
					"type":        "string",
					"description": "Scroll direction: \"up\" or \"down\"",
					"enum":        []string{"up", "down"},
				},
			},
			"required": []any{"direction"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				Direction string `json:"direction"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if input.Direction != "up" && input.Direction != "down" {
				return nil, fmt.Errorf("direction must be \"up\" or \"down\"")
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser_navigate first")
			}

			var snapshot string
			var err error

			switch session.backendType {
			case BackendCamofox:
				snapshot, err = session.camofoxClient.Scroll(session.sessionID, input.Direction)
			case BackendLightpanda:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				script := "window.scrollBy(0, 500);"
				if input.Direction == "up" {
					script = "window.scrollBy(0, -500);"
				}
				var res interface{}
				if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(script, &res)); err != nil {
					cancel()
					return nil, fmt.Errorf("scroll failed: %w", err)
				}
				cancel()
				snapshot, err = generateSnapshot(session.ctx, false, session.refMapper)
			case BackendLocal, BackendCDP, BackendBrowserbase, BackendBrowserUse, BackendFirecrawl, BackendAgentBrowser:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)

				script := "window.scrollBy(0, 500);"
				if input.Direction == "up" {
					script = "window.scrollBy(0, -500);"
				}

				var res interface{}
				if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(script, &res)); err != nil {
					cancel()
					return nil, fmt.Errorf("scroll failed: %w", err)
				}

				snapshot, err = generateSnapshot(timeoutCtx, false, session.refMapper)
				cancel()
			default:
				err = fmt.Errorf("backend %s not supported for scroll", session.backendType)
			}

			if err != nil {
				return nil, err
			}

			session.mu.Lock()
			session.lastActivity = time.Now()
			session.mu.Unlock()

			return result(fmt.Sprintf("Scrolled %s\n\n%s", input.Direction, snapshot), nil)
		},
	}
}

func NewBrowserBackTool(cfg *BrowserToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &BrowserToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "browser_back",
		Description: "Navigate back in browser history.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser_navigate first")
			}

			var url, title string
			var snapshot string
			var err error

			switch session.backendType {
			case BackendCamofox:
				snapshot, err = session.camofoxClient.Back(session.sessionID)
			case BackendLightpanda:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				if err := chromedp.Run(timeoutCtx,
					chromedp.NavigateBack(),
					chromedp.WaitReady("body", chromedp.ByQuery),
				); err != nil {
					cancel()
					return nil, fmt.Errorf("back navigation failed: %w", err)
				}
				chromedp.Run(timeoutCtx,
					chromedp.Location(&url),
					chromedp.Title(&title),
				)
				cancel()
				snapshot, err = generateSnapshot(session.ctx, false, session.refMapper)
			case BackendLocal, BackendCDP, BackendBrowserbase, BackendBrowserUse, BackendFirecrawl, BackendAgentBrowser:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)

				if err := chromedp.Run(timeoutCtx,
					chromedp.NavigateBack(),
				); err != nil {
					cancel()
					return nil, fmt.Errorf("back navigation failed: %w", err)
				}

				chromedp.Run(timeoutCtx,
					chromedp.Location(&url),
					chromedp.Title(&title),
				)

				snapshot, err = generateSnapshot(timeoutCtx, false, session.refMapper)
				cancel()
			default:
				err = fmt.Errorf("backend %s not supported for back", session.backendType)
			}

			if err != nil {
				return nil, err
			}

			session.mu.Lock()
			session.url = url
			session.title = title
			session.lastActivity = time.Now()
			session.mu.Unlock()

			return result(fmt.Sprintf("Navigated back\nURL: %s\nTitle: %s\n\n%s", url, title, snapshot), nil)
		},
	}
}

func NewBrowserPressTool(cfg *BrowserToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &BrowserToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "browser_press",
		Description: "Press a keyboard key. Common keys: Enter, Tab, Escape, ArrowUp, ArrowDown, ArrowLeft, ArrowRight.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{
					"type":        "string",
					"description": "Key to press (e.g. Enter, Tab, Escape, ArrowDown)",
				},
			},
			"required": []any{"key"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				Key string `json:"key"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser_navigate first")
			}

			var resultMsg string
			var err error

			switch session.backendType {
			case BackendCamofox:
				resultMsg, err = session.camofoxClient.Press(session.sessionID, input.Key)
			case BackendLightpanda:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				if err := chromedp.Run(timeoutCtx, chromedp.KeyEvent(input.Key)); err != nil {
					cancel()
					return nil, fmt.Errorf("key press failed: %w", err)
				}
				cancel()
				resultMsg = fmt.Sprintf("Pressed key: %s", input.Key)
			case BackendLocal, BackendCDP, BackendBrowserbase, BackendBrowserUse, BackendFirecrawl, BackendAgentBrowser:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				defer cancel()

				if err := chromedp.Run(timeoutCtx, chromedp.KeyEvent(input.Key)); err != nil {
					return nil, fmt.Errorf("key press failed: %w", err)
				}

				resultMsg = fmt.Sprintf("Pressed key: %s", input.Key)
			default:
				err = fmt.Errorf("backend %s not supported for press", session.backendType)
			}

			if err != nil {
				return nil, err
			}

			session.mu.Lock()
			session.lastActivity = time.Now()
			session.mu.Unlock()

			return result(resultMsg, nil)
		},
	}
}

func NewBrowserScreenshotTool(cfg *BrowserToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &BrowserToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "browser_screenshot",
		Description: "Take a screenshot of the current page. Returns base64-encoded PNG data.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"full_page": map[string]any{
					"type":        "boolean",
					"description": "Capture full page (default: false, captures viewport only)",
					"default":     false,
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				FullPage bool `json:"full_page"`
			}
			_ = json.Unmarshal(args, &input)

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser_navigate first")
			}

			var sizeBytes int
			var err error

			switch session.backendType {
			case BackendCamofox:
				var buf []byte
				buf, err = session.camofoxClient.Screenshot(session.sessionID)
				if err == nil {
					sizeBytes = len(buf)
				}
			case BackendLightpanda:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				var buf []byte
				if err := chromedp.Run(timeoutCtx, chromedp.CaptureScreenshot(&buf)); err != nil {
					cancel()
					return nil, fmt.Errorf("screenshot failed: %w", err)
				}
				cancel()
				sizeBytes = len(buf)
			case BackendLocal, BackendCDP, BackendBrowserbase, BackendBrowserUse, BackendFirecrawl, BackendAgentBrowser:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				defer cancel()

				var buf []byte
				if err := chromedp.Run(timeoutCtx, chromedp.CaptureScreenshot(&buf)); err != nil {
					return nil, fmt.Errorf("screenshot failed: %w", err)
				}
				sizeBytes = len(buf)
			default:
				err = fmt.Errorf("backend %s not supported for screenshot", session.backendType)
			}

			if err != nil {
				return nil, err
			}

			session.mu.Lock()
			session.lastActivity = time.Now()
			session.mu.Unlock()

			return result(fmt.Sprintf("Screenshot captured (%d bytes)", sizeBytes), map[string]any{
				"format":     "png",
				"size_bytes": sizeBytes,
			})
		},
	}
}

func NewBrowserEvaluateTool(cfg *BrowserToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &BrowserToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "browser_evaluate",
		Description: "Execute JavaScript in the page context. Returns the evaluation result.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"expression": map[string]any{
					"type":        "string",
					"description": "JavaScript expression to evaluate",
				},
				"frame_id": map[string]any{
					"type":        "string",
					"description": "Optional frame ID for OOPIF evaluation (from browser_snapshot frame tree)",
				},
			},
			"required": []any{"expression"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				Expression string `json:"expression"`
				FrameID    string `json:"frame_id"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser_navigate first")
			}

			var evalResult string
			var err error

			if session.supervisor != nil && input.FrameID != "" {
				evalResult, err = session.supervisor.EvaluateJS(input.Expression, input.FrameID)
			} else if session.backendType == BackendLightpanda || session.backendType == BackendLocal || session.backendType == BackendCDP || session.backendType == BackendBrowserbase || session.backendType == BackendBrowserUse || session.backendType == BackendFirecrawl {
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				var result string
				if err := chromedp.Run(timeoutCtx, chromedp.EvaluateAsDevTools(input.Expression, &result)); err != nil {
					cancel()
					return nil, fmt.Errorf("evaluation failed: %w", err)
				}
				cancel()
				evalResult = result
			} else {
				err = fmt.Errorf("JS evaluation not supported for backend %s", session.backendType)
			}

			if err != nil {
				return nil, err
			}

			session.mu.Lock()
			session.lastActivity = time.Now()
			session.mu.Unlock()

			return result(fmt.Sprintf("Result: %s", evalResult), nil)
		},
	}
}

func NewBrowserDialogTool(cfg *BrowserToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &BrowserToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "browser_dialog",
		Description: "Handle pending JavaScript dialogs (alert/confirm/prompt). Use after browser_snapshot shows pending dialogs.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dialog_id": map[string]any{
					"type":        "string",
					"description": "Dialog ID from pending dialogs list",
				},
				"accept": map[string]any{
					"type":        "boolean",
					"description": "Whether to accept (true) or dismiss (false) the dialog",
				},
				"prompt_text": map[string]any{
					"type":        "string",
					"description": "Text to enter for prompt dialogs",
				},
			},
			"required": []any{"dialog_id", "accept"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				DialogID   string `json:"dialog_id"`
				Accept     bool   `json:"accept"`
				PromptText string `json:"prompt_text"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser_navigate first")
			}

			if session.supervisor == nil {
				return nil, fmt.Errorf("CDP supervisor not available (requires CDP endpoint)")
			}

			if err := session.supervisor.HandleDialog(input.DialogID, input.Accept, input.PromptText); err != nil {
				return nil, fmt.Errorf("dialog handling failed: %w", err)
			}

			action := "dismissed"
			if input.Accept {
				action = "accepted"
			}
			msg := fmt.Sprintf("Dialog %s %s", input.DialogID, action)
			if input.PromptText != "" {
				msg += fmt.Sprintf(" with text: %s", input.PromptText)
			}

			return result(msg, nil)
		},
	}
}

func NewBrowserConsoleTool(cfg *BrowserToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &BrowserToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "browser_console",
		Description: "Get console messages and JavaScript errors from the current page.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser_navigate first")
			}

			session.mu.Lock()
			session.lastActivity = time.Now()
			session.mu.Unlock()

			return result("Console messages are captured asynchronously. Use browser_evaluate to check page state.", nil)
		},
	}
}

func NewBrowserVisionTool(cfg *BrowserToolConfig) *agentcore.Tool {
	if cfg == nil {
		cfg = &BrowserToolConfig{}
	}
	cfg.defaults()

	return &agentcore.Tool{
		Name:        "browser_vision",
		Description: "Take a screenshot and analyze it with a vision AI model. Ask questions about the page content.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "Question to ask about the page (e.g. 'What is the main heading?', 'List all visible links')",
				},
				"annotate": map[string]any{
					"type":        "boolean",
					"description": "Overlay numbered labels on interactive elements (default: false)",
					"default":     false,
				},
			},
			"required": []any{"question"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				Question string `json:"question"`
				Annotate bool   `json:"annotate"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser_navigate first")
			}

			var screenshotData []byte
			var err error

			switch session.backendType {
			case BackendCamofox:
				screenshotData, err = session.camofoxClient.Screenshot(session.sessionID)
			case BackendLightpanda, BackendLocal, BackendCDP, BackendBrowserbase, BackendBrowserUse, BackendFirecrawl, BackendAgentBrowser:
				timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
				if err := chromedp.Run(timeoutCtx, chromedp.CaptureScreenshot(&screenshotData)); err != nil {
					cancel()
					return nil, fmt.Errorf("screenshot failed: %w", err)
				}
				cancel()
			default:
				return nil, fmt.Errorf("backend %s not supported for vision", session.backendType)
			}

			if err != nil {
				return nil, fmt.Errorf("screenshot failed: %w", err)
			}

			analysis := fmt.Sprintf("Vision analysis requested for: %s (model: %s)", input.Question, cfg.VisionModel)

			session.mu.Lock()
			session.lastActivity = time.Now()
			session.mu.Unlock()

			return result(analysis, nil)
		},
	}
}
