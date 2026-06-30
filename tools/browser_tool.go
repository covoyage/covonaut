package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/covoyage/covonaut/agentcore"
)

func NewBrowserTool(cfg *BrowserToolConfig) *agentcore.Tool {
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
		Name:        "browser",
		Description: "Control a web browser. Use this for user-provided URLs, interactive pages, login flows, JavaScript-heavy pages, or as fallback when web_fetch is blocked. For simple information retrieval, prefer web_search (faster, cheaper, no browser overhead). Actions: navigate (open URL), snapshot (get page text with interactive elements), click (click element by ref ID), type (type text into element by ref ID), scroll (up/down), back (history back), press (keyboard key), screenshot (viewport capture), evaluate (run JS), dialog (handle alert/confirm/prompt), vision (ask AI about page screenshot), console (retrieve console logs).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "The browser action to perform",
					"enum":        []string{"navigate", "snapshot", "click", "type", "scroll", "back", "press", "screenshot", "evaluate", "dialog", "vision", "console", "cdp", "get_images"},
				},
				"url": map[string]any{
					"type":        "string",
					"description": "URL to navigate to (required for action=navigate)",
				},
				"ref": map[string]any{
					"type":        "string",
					"description": "Element ref ID from snapshot (e.g. @e5 or e5, required for action=click and action=type)",
				},
				"text": map[string]any{
					"type":        "string",
					"description": "Text to type into the field (required for action=type)",
				},
				"direction": map[string]any{
					"type":        "string",
					"description": "Scroll direction (required for action=scroll)",
					"enum":        []string{"up", "down"},
				},
				"key": map[string]any{
					"type":        "string",
					"description": "Key to press (required for action=press). Common keys: Enter, Tab, Escape, ArrowUp, ArrowDown, ArrowLeft, ArrowRight.",
				},
				"full_page": map[string]any{
					"type":        "boolean",
					"description": "Capture full page screenshot (for action=screenshot, default: false, captures viewport only)",
				},
				"full": map[string]any{
					"type":        "boolean",
					"description": "Show complete page content (for action=snapshot, default: false, shows only interactive elements)",
				},
				"mode": map[string]any{
					"type":        "string",
					"description": "Snapshot mode (for action=snapshot). \"default\": JS-based accessibility tree with XPath refs. \"aria\": Chrome native aria tree with richer role/name info. (default: \"default\")",
					"enum":        []string{"default", "aria"},
				},
				"expression": map[string]any{
					"type":        "string",
					"description": "JavaScript expression to evaluate (required for action=evaluate)",
				},
				"frame_id": map[string]any{
					"type":        "string",
					"description": "Optional frame ID for OOPIF evaluation (from snapshot frame tree, for action=evaluate)",
				},
				"dialog_id": map[string]any{
					"type":        "string",
					"description": "Dialog ID from pending dialogs list (required for action=dialog)",
				},
				"accept": map[string]any{
					"type":        "boolean",
					"description": "Whether to accept (true) or dismiss (false) the dialog (required for action=dialog)",
				},
				"prompt_text": map[string]any{
					"type":        "string",
					"description": "Text to enter for prompt dialogs (for action=dialog)",
				},
				"question": map[string]any{
					"type":        "string",
					"description": "Question to ask about the page (required for action=vision)",
				},
				"annotate": map[string]any{
					"type":        "boolean",
					"description": "Overlay numbered labels on interactive elements (for action=vision, default: false)",
				},
				"cdp_method": map[string]any{
					"type":        "string",
					"description": "Chrome DevTools Protocol method (required for action=cdp, e.g. Page.captureScreenshot, Runtime.evaluate, DOM.getDocument)",
				},
				"cdp_params": map[string]any{
					"type":        "object",
					"description": "CDP method parameters as JSON object (for action=cdp)",
				},
			},
			"required": []any{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				Action     string `json:"action"`
				URL        string `json:"url"`
				Ref        string `json:"ref"`
				Text       string `json:"text"`
				Direction  string `json:"direction"`
				Key        string `json:"key"`
				FullPage   bool   `json:"full_page"`
				Full       bool   `json:"full"`
				Mode       string `json:"mode"`
				Expression string `json:"expression"`
				FrameID    string `json:"frame_id"`
				DialogID   string `json:"dialog_id"`
				Accept     bool   `json:"accept"`
				PromptText string `json:"prompt_text"`
				Question   string `json:"question"`
				Annotate   bool   `json:"annotate"`
				CDPMethod  string                 `json:"cdp_method"`
				CDPParams  map[string]interface{} `json:"cdp_params"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch input.Action {
			case "navigate":
				if input.URL == "" {
					return nil, fmt.Errorf("url is required for action=navigate")
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

					time.Sleep(1 * time.Second)

					stealthCtx, stealthCancel := context.WithTimeout(timeoutCtx, 3*time.Second)
					chromedp.Run(stealthCtx, chromedp.Evaluate(stealthJavaScript, nil))
					stealthCancel()

					var title string
					titleCtx, titleCancel := context.WithTimeout(timeoutCtx, 5*time.Second)
					chromedp.Run(titleCtx, chromedp.Title(&title))
					titleCancel()

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

				time.Sleep(1 * time.Second)

				stealthCtx, stealthCancel := context.WithTimeout(timeoutCtx, 3*time.Second)
				chromedp.Run(stealthCtx, chromedp.Evaluate(stealthJavaScript, nil))
				stealthCancel()

				var title string
				titleCtx, titleCancel := context.WithTimeout(timeoutCtx, 5*time.Second)
				chromedp.Run(titleCtx, chromedp.Title(&title))
				titleCancel()

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

			case "snapshot":
			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser (action=navigate) first")
			}

			mode := input.Mode
			if mode == "" {
				mode = "default"
			}

			var err error
			var snapshot string
				switch session.backendType {
				case BackendCamofox:
					snapshot, err = session.camofoxClient.GetSnapshot(session.sessionID, input.Full)
				case BackendLightpanda, BackendLocal, BackendCDP, BackendBrowserbase, BackendBrowserUse, BackendFirecrawl, BackendAgentBrowser:
					timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
					snapshot, err = GeneratePageSnapshot(timeoutCtx, input.Full, session.refMapper, mode)
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

			case "click":
			if input.Ref == "" {
				return nil, fmt.Errorf("ref is required for action=click")
			}
			ref := input.Ref
			if !strings.HasPrefix(ref, "@") {
				ref = "@" + ref
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser (action=navigate) first")
			}

			var err error
			var snapshot string
				switch session.backendType {
				case BackendCamofox:
					snapshot, err = session.camofoxClient.Click(session.sessionID, ref)
				case BackendLightpanda:
					timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
					xpath, lookupErr := session.refMapper.Get(ref)
					if lookupErr {
						var jsXpath string
						js := fmt.Sprintf(`window.__covoRefMap && window.__covoRefMap[%q] || null`, ref)
						if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(js, &jsXpath)); err == nil && jsXpath != "" {
							xpath = jsXpath
							session.refMapper.Set(ref, xpath)
						} else {
							cancel()
							if session.refMapper.Count() == 0 {
								return nil, fmt.Errorf("ref %s not found. Page state is unknown. Call browser (action=snapshot) first", ref)
							}
							return nil, fmt.Errorf("ref %s not found in current page state. Call browser (action=snapshot) to refresh", ref)
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
						var jsXpath string
						js := fmt.Sprintf(`window.__covoRefMap && window.__covoRefMap[%q] || null`, ref)
						if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(js, &jsXpath)); err == nil && jsXpath != "" {
							xpath = jsXpath
							session.refMapper.Set(ref, xpath)
						} else {
							cancel()
							if session.refMapper.Count() == 0 {
								return nil, fmt.Errorf("ref %s not found. Page state is unknown. Call browser (action=snapshot) first", ref)
							}
							return nil, fmt.Errorf("ref %s not found in current page state. Call browser (action=snapshot) to refresh", ref)
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

			case "type":
			if input.Ref == "" || input.Text == "" {
				return nil, fmt.Errorf("ref and text are required for action=type")
			}
			ref := input.Ref
			if !strings.HasPrefix(ref, "@") {
				ref = "@" + ref
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser (action=navigate) first")
			}

			var err error
			var resultMsg string
				switch session.backendType {
				case BackendCamofox:
					resultMsg, err = session.camofoxClient.Type(session.sessionID, ref, input.Text)
				case BackendLightpanda:
					timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
					xpath, lookupErr := session.refMapper.Get(ref)
					if lookupErr {
						var jsXpath string
						js := fmt.Sprintf(`window.__covoRefMap && window.__covoRefMap[%q] || null`, ref)
						if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(js, &jsXpath)); err == nil && jsXpath != "" {
							xpath = jsXpath
							session.refMapper.Set(ref, xpath)
						} else {
							cancel()
							if session.refMapper.Count() == 0 {
								return nil, fmt.Errorf("ref %s not found. Page state is unknown. Call browser (action=snapshot) first", ref)
							}
							return nil, fmt.Errorf("ref %s not found in current page state. Call browser (action=snapshot) to refresh", ref)
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
						var jsXpath string
						js := fmt.Sprintf(`window.__covoRefMap && window.__covoRefMap[%q] || null`, ref)
						if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(js, &jsXpath)); err == nil && jsXpath != "" {
							xpath = jsXpath
							session.refMapper.Set(ref, xpath)
						} else {
							cancel()
							if session.refMapper.Count() == 0 {
								return nil, fmt.Errorf("ref %s not found. Page state is unknown. Call browser (action=snapshot) first", ref)
							}
							return nil, fmt.Errorf("ref %s not found in current page state. Call browser (action=snapshot) to refresh", ref)
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

			case "scroll":
			if input.Direction != "up" && input.Direction != "down" {
				return nil, fmt.Errorf("direction must be \"up\" or \"down\" for action=scroll")
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser (action=navigate) first")
			}

			var err error
			var snapshot string
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

			case "back":
			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser (action=navigate) first")
			}

			var err error
			var url, title string
			var snapshot string
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

			case "press":
			if input.Key == "" {
				return nil, fmt.Errorf("key is required for action=press")
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser (action=navigate) first")
			}

			var err error
			var resultMsg string
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

			case "screenshot":
			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser (action=navigate) first")
			}

			var err error
			var sizeBytes int
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

			case "evaluate":
			if input.Expression == "" {
				return nil, fmt.Errorf("expression is required for action=evaluate")
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser (action=navigate) first")
			}

			var err error
			var evalResult string
				if session.supervisor != nil && input.FrameID != "" {
					evalResult, err = session.supervisor.EvaluateJS(input.Expression, input.FrameID)
				} else if session.backendType == BackendLightpanda || session.backendType == BackendLocal || session.backendType == BackendCDP || session.backendType == BackendBrowserbase || session.backendType == BackendBrowserUse || session.backendType == BackendFirecrawl {
					timeoutCtx, cancel := context.WithTimeout(session.ctx, cfg.CommandTimeout)
					var result string
					// Use Evaluate (not EvaluateAsDevTools) — it handles array returns gracefully via JSON.stringify
					if err := chromedp.Run(timeoutCtx, chromedp.Evaluate(input.Expression, &result)); err != nil {
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

			case "dialog":
				if input.DialogID == "" {
					return nil, fmt.Errorf("dialog_id is required for action=dialog")
				}

				session, ok := defaultBrowserManager.GetActiveSession("default")
				if !ok {
					return nil, fmt.Errorf("no active browser session. Call browser (action=navigate) first")
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

			case "vision":
			if input.Question == "" {
				return nil, fmt.Errorf("question is required for action=vision")
			}

			session, ok := defaultBrowserManager.GetActiveSession("default")
			if !ok {
				return nil, fmt.Errorf("no active browser session. Call browser (action=navigate) first")
			}

			var err error
			var screenshotData []byte
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

			case "console":
				session, ok := defaultBrowserManager.GetActiveSession("default")
				if !ok {
					return nil, fmt.Errorf("no active browser session. Call browser (action=navigate) first")
				}

				session.mu.Lock()
				session.lastActivity = time.Now()
				session.mu.Unlock()

				return result("Console messages are captured asynchronously. Use browser (action=evaluate) to check page state.", nil)

			case "cdp":
				session, ok := defaultBrowserManager.GetActiveSession("default")
				if !ok {
					return nil, fmt.Errorf("no active browser session. Call browser (action=navigate) first")
				}
				if input.CDPMethod == "" {
					return nil, fmt.Errorf("cdp_method is required for action=cdp")
				}
				session.mu.Lock()
				session.lastActivity = time.Now()
				session.mu.Unlock()

				var cdpResult interface{}
				err := chromedp.Run(ctx,
					chromedp.ActionFunc(func(ctx context.Context) error {
						return chromedp.Evaluate(fmt.Sprintf(`
							(async () => {
								const result = await chrome.send('%s', %s);
								return JSON.stringify(result);
							})()
						`, input.CDPMethod, cdpParamsToJSON(input.CDPParams)), &cdpResult).Do(ctx)
					}),
				)
				if err != nil {
					data, _ := json.Marshal(map[string]any{"error": err.Error()})
					return result(string(data), nil)
				}
				data, _ := json.Marshal(map[string]any{"result": cdpResult})
				return result(string(data), nil)

			case "get_images":
				session, ok := defaultBrowserManager.GetActiveSession("default")
				if !ok {
					return nil, fmt.Errorf("no active browser session. Call browser (action=navigate) first")
				}
				session.mu.Lock()
				session.lastActivity = time.Now()
				session.mu.Unlock()

				var imagesJSON string
				err := chromedp.Run(ctx,
					chromedp.Evaluate(`(() => {
						const imgs = [];
						document.querySelectorAll('img').forEach((img, i) => {
							if (img.src) { imgs.push({index: i, src: img.src, alt: img.alt || '', size: (img.naturalWidth || 0) + 'x' + (img.naturalHeight || 0)}); }
							if (imgs.length >= 20) return;
						});
						return JSON.stringify(imgs);
					})()`, &imagesJSON),
				)
				if err != nil {
					data, _ := json.Marshal(map[string]any{"error": err.Error()})
					return result(string(data), nil)
				}
				return result(fmt.Sprintf("Images on page (%d max):\n%s", 20, imagesJSON), nil)

			default:
				return nil, fmt.Errorf("unknown browser action: %s. Valid actions: navigate, snapshot, click, type, scroll, back, press, screenshot, evaluate, dialog, vision, console, cdp, get_images", input.Action)
			}
		},
	}
}

func cdpParamsToJSON(params map[string]interface{}) string {
	if params == nil {
		return "{}"
	}
	b, _ := json.Marshal(params)
	return string(b)
}
