package tools

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/covoyage/covonaut/tools/browser_providers"
)

var privateIPBlocks []*net.IPNet

func init() {
	for _, cidr := range []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	} {
		_, block, err := net.ParseCIDR(cidr)
		if err == nil {
			privateIPBlocks = append(privateIPBlocks, block)
		}
	}
}

func isPrivateIP(ip net.IP) bool {
	for _, block := range privateIPBlocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

func isMetadataEndpoint(host string) bool {
	lower := strings.ToLower(host)
	return lower == "169.254.169.254" ||
		lower == "metadata.google.internal" ||
		lower == "metadata.azure.com" ||
		lower == "169.254.170.2" ||
		strings.HasSuffix(lower, ".internal")
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|secret[_-]?key|access[_-]?token|auth[_-]?token|password|passwd)=`),
	regexp.MustCompile(`(?i)(sk-[a-zA-Z0-9]{20,})`),
	regexp.MustCompile(`(?i)(ghp_[a-zA-Z0-9]{36})`),
}

func containsSecretInURL(rawURL string) bool {
	for _, re := range secretPatterns {
		if re.MatchString(rawURL) {
			return true
		}
	}
	return false
}

func validateURL(rawURL string, allowPrivate bool) (*url.URL, error) {
	if rawURL == "" {
		return nil, fmt.Errorf("URL cannot be empty")
	}

	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("only http/https URLs are allowed, got scheme: %s", parsed.Scheme)
	}

	if containsSecretInURL(rawURL) {
		return nil, fmt.Errorf("URL appears to contain secrets in query parameters")
	}

	if isMetadataEndpoint(parsed.Hostname()) {
		return nil, fmt.Errorf("access to cloud metadata endpoints is blocked")
	}

	if !allowPrivate {
		host := parsed.Hostname()
		if ip := net.ParseIP(host); ip != nil {
			if isPrivateIP(ip) {
				return nil, fmt.Errorf("access to private IP addresses is not allowed (%s)", host)
			}
		}
	}

	return parsed, nil
}

func IsPrivateURL(rawURL string) bool {
	parsed, err := validateURL(rawURL, true)
	if err != nil {
		return false
	}

	host := parsed.Hostname()
	if isMetadataEndpoint(host) {
		return true
	}

	if ip := net.ParseIP(host); ip != nil {
		return isPrivateIP(ip)
	}

	return false
}

type BrowserSession struct {
	mu             sync.RWMutex
	sessionID      string
	backendType    BrowserBackendType
	cdpURL         string
	cloudProvider  browser_providers.CloudBrowserProvider
	cloudSessionID string
	camofoxClient  *CamofoxClient
	lightpandaProc *LightpandaProcess
	ctx            context.Context
	cancel         context.CancelFunc
	url            string
	title          string
	createdAt      time.Time
	lastActivity   time.Time
	supervisor     *CDPSupervisor
	recorder       *CDPRecorder
	refMapper      *RefMapper
}

type BrowserManager struct {
	mu                    sync.RWMutex
	sessions              map[string]*BrowserSession
	config                BrowserConfig
	cloudProvider         browser_providers.CloudBrowserProvider
	camofoxClient         *CamofoxClient
	lightpandaMgr         *LightpandaManager
	agentBrowserMgr       *AgentBrowserManager
	fallbackCloudProviders []browser_providers.CloudBrowserProvider
	activeSession         string
}

func NewBrowserManager(cfg *BrowserConfig) *BrowserManager {
	cfg.defaults()

	mgr := &BrowserManager{
		sessions: make(map[string]*BrowserSession),
		config:   *cfg,
	}

	backend := DetectBackend(&mgr.config)
	switch backend {
	case BackendBrowserbase:
		mgr.cloudProvider = browser_providers.NewBrowserbaseProvider()
	case BackendBrowserUse:
		mgr.cloudProvider = browser_providers.NewBrowserUseProvider()
	case BackendFirecrawl:
		mgr.cloudProvider = browser_providers.NewFirecrawlProvider()
	case BackendCamofox:
		mgr.camofoxClient = NewCamofoxClient(CamofoxConfig{
			BaseURL:    cfg.CamofoxURL,
			UserID:     os.Getenv("CAMOFOX_USER_ID"),
			SessionKey: os.Getenv("CAMOFOX_SESSION_KEY"),
		})
	case BackendLightpanda:
		mgr.lightpandaMgr = NewLightpandaManager()
	case BackendAgentBrowser:
		mgr.agentBrowserMgr = NewAgentBrowserManager()
	}

	if backend == BackendAgentBrowser {
		return mgr
	}

	if backend == BackendLocal || backend == BackendLightpanda {
		for _, try := range []struct {
			name     string
			factory  func() browser_providers.CloudBrowserProvider
		}{
			{"browserbase", func() browser_providers.CloudBrowserProvider { return browser_providers.NewBrowserbaseProvider() }},
			{"browser_use", func() browser_providers.CloudBrowserProvider { return browser_providers.NewBrowserUseProvider() }},
			{"firecrawl", func() browser_providers.CloudBrowserProvider { return browser_providers.NewFirecrawlProvider() }},
		} {
			if try.name == cfg.CloudProvider {
				continue // already the primary provider
			}
			p := try.factory()
			if p != nil && p.IsConfigured() {
				mgr.fallbackCloudProviders = append(mgr.fallbackCloudProviders, p)
			}
		}
	}

	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			mgr.CleanupInactiveSessions(cfg.InactivityTimeout)
		}
	}()

	return mgr
}

func (bm *BrowserManager) GetSession(sessionID string) (*BrowserSession, bool) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	s, ok := bm.sessions[sessionID]
	return s, ok
}

func (bm *BrowserManager) GetActiveSession(fallbackSessionID string) (*BrowserSession, bool) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	if bm.activeSession != "" {
		if s, ok := bm.sessions[bm.activeSession]; ok {
			return s, true
		}
	}
	s, ok := bm.sessions[fallbackSessionID]
	return s, ok
}

func (bm *BrowserManager) SetActiveSession(sessionID string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if s, ok := bm.sessions[sessionID]; ok {
		bm.activeSession = sessionID
		s.lastActivity = time.Now()
	}
}

func (bm *BrowserManager) CreateSession(ctx context.Context, sessionID string, targetURL string) (*BrowserSession, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if existing, ok := bm.sessions[sessionID]; ok {
		bm.activeSession = sessionID
		existing.lastActivity = time.Now()
		return existing, nil
	}

	backend := DetectBackend(&bm.config)

	if bm.config.AutoLocalForPrivate && bm.cloudProvider != nil && targetURL != "" {
		if IsPrivateURL(targetURL) {
			backend = BackendLocal
			sessionID = sessionID + "::local"
		}
	}
	if existing, ok := bm.sessions[sessionID]; ok {
		bm.activeSession = sessionID
		existing.lastActivity = time.Now()
		return existing, nil
	}

	session := &BrowserSession{
		sessionID:    sessionID,
		backendType:  backend,
		createdAt:    time.Now(),
		lastActivity: time.Now(),
		refMapper:    NewRefMapper(),
	}

	var err error
	switch backend {
	case BackendCDP:
		err = bm.createCDPSession(ctx, session)
	case BackendCamofox:
		err = bm.createCamofoxSession(ctx, session, targetURL)
	case BackendLightpanda:
		err = bm.createLightpandaSession(ctx, session, targetURL)
	case BackendBrowserbase, BackendBrowserUse, BackendFirecrawl:
		err = bm.createCloudSession(ctx, session)
	case BackendAgentBrowser:
		err = bm.createAgentBrowserSession(ctx, session)
	case BackendLocal:
		err = bm.createLocalSession(ctx, session)
	default:
		err = bm.createLocalSession(ctx, session)
	}

	if err != nil && len(bm.fallbackCloudProviders) > 0 {
		// Primary backend failed; try cloud providers as fallback.
		for _, fp := range bm.fallbackCloudProviders {
			providerName := fp.ProviderName()
			session.backendType = BackendBrowserbase // generic cloud type
			fallbackErr := bm.createCloudSessionWithProvider(ctx, session, fp)
			if fallbackErr == nil {
				fmt.Fprintf(os.Stderr, "[browser] fallback: %s took over after %s failed (%v)\n", providerName, backend, err)
				err = nil
				break
			}
			fmt.Fprintf(os.Stderr, "[browser] fallback %s also failed (%v)\n", providerName, fallbackErr)
		}
	}

	if err != nil {
		return nil, err
	}

	if bm.config.RecordSessions && bm.config.RecordingDir != "" {
		session.recorder = NewCDPRecorder(bm.config.RecordingDir)
	}

	bm.sessions[sessionID] = session
	bm.activeSession = sessionID
	return session, nil
}

func (bm *BrowserManager) createCDPSession(ctx context.Context, session *BrowserSession) error {
	session.backendType = BackendCDP
	session.cdpURL = bm.config.CDPURL
	if session.cdpURL == "" {
		return fmt.Errorf("CDP URL is required")
	}

	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), session.cdpURL)
	browserCtx, cancel := chromedp.NewContext(allocCtx)
	session.ctx = browserCtx
	session.cancel = func() {
		cancel()
		allocCancel()
	}

	if bm.config.DialogPolicy != "" {
		supervisor := NewCDPSupervisor(bm.config.CDPURL, session.sessionID, bm.config.DialogPolicy, bm.config.DialogTimeout)
		if err := supervisor.Start(ctx); err != nil {
			return fmt.Errorf("CDP supervisor failed: %w", err)
		}
		session.supervisor = supervisor
	}

	return nil
}

func (bm *BrowserManager) createCamofoxSession(ctx context.Context, session *BrowserSession, targetURL string) error {
	if bm.camofoxClient == nil {
		bm.camofoxClient = CamofoxFromEnv()
	}
	if bm.camofoxClient == nil {
		return fmt.Errorf("camofox not configured")
	}

	tab, err := bm.camofoxClient.CreateTab(session.sessionID, targetURL)
	if err != nil {
		return fmt.Errorf("camofox create tab failed: %w", err)
	}

	session.backendType = BackendCamofox
	session.camofoxClient = bm.camofoxClient
	session.url = tab.URL

	return nil
}

func (bm *BrowserManager) createLightpandaSession(ctx context.Context, session *BrowserSession, targetURL string) error {
	if bm.lightpandaMgr == nil {
		bm.lightpandaMgr = NewLightpandaManager()
	}

	proc, err := bm.lightpandaMgr.StartProcess(context.Background(), session.sessionID, bm.config.Headless)
	if err != nil {
		return fmt.Errorf("lightpanda start failed: %w", err)
	}

	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), proc.CDPURL)
	browserCtx, cancel := chromedp.NewContext(allocCtx)

	session.backendType = BackendLightpanda
	session.lightpandaProc = proc
	session.cdpURL = proc.CDPURL
	session.ctx = browserCtx
	session.cancel = func() {
		cancel()
		allocCancel()
	}

	if bm.config.DialogPolicy != "" {
		supervisor := NewCDPSupervisor(proc.CDPURL, session.sessionID, bm.config.DialogPolicy, bm.config.DialogTimeout)
		if err := supervisor.Start(context.Background()); err != nil {
			// Supervisor is optional for lightpanda
		} else {
			session.supervisor = supervisor
		}
	}

	if targetURL != "" {
		timeoutCtx, cancel := context.WithTimeout(browserCtx, bm.config.CommandTimeout)
		if err := chromedp.Run(timeoutCtx, chromedp.Navigate(targetURL)); err != nil {
			cancel()
			return fmt.Errorf("lightpanda navigation failed: %w", err)
		}
		cancel()
		session.url = targetURL
	}

	return nil
}

func (bm *BrowserManager) createCloudSession(ctx context.Context, session *BrowserSession) error {
	if bm.cloudProvider == nil || !bm.cloudProvider.IsConfigured() {
		return fmt.Errorf("cloud provider not configured")
	}

	result, err := bm.cloudProvider.CreateSession(session.sessionID)
	if err != nil {
		return fmt.Errorf("cloud session creation failed: %w", err)
	}

	session.backendType = DetectBackend(&bm.config)
	session.cloudProvider = bm.cloudProvider
	session.cloudSessionID = result["session_id"]
	session.cdpURL = result["cdp_url"]
	if session.cdpURL == "" {
		return fmt.Errorf("cloud provider did not return a CDP URL")
	}

	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), session.cdpURL)
	browserCtx, cancel := chromedp.NewContext(allocCtx)
	session.ctx = browserCtx
	session.cancel = func() {
		cancel()
		allocCancel()
	}

	if session.cdpURL != "" && bm.config.DialogPolicy != "" {
		supervisor := NewCDPSupervisor(session.cdpURL, session.sessionID, bm.config.DialogPolicy, bm.config.DialogTimeout)
		if err := supervisor.Start(ctx); err == nil {
			session.supervisor = supervisor
		}
	}

	return nil
}

func (bm *BrowserManager) createCloudSessionWithProvider(ctx context.Context, session *BrowserSession, provider browser_providers.CloudBrowserProvider) error {
	if provider == nil || !provider.IsConfigured() {
		return fmt.Errorf("cloud provider not configured")
	}

	result, err := provider.CreateSession(session.sessionID)
	if err != nil {
		return fmt.Errorf("cloud session creation failed: %w", err)
	}

	session.cloudProvider = provider
	session.cloudSessionID = result["session_id"]
	session.cdpURL = result["cdp_url"]

	if session.cdpURL == "" {
		return fmt.Errorf("cloud provider did not return a CDP URL")
	}

	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), session.cdpURL)
	browserCtx, cancel := chromedp.NewContext(allocCtx)
	session.ctx = browserCtx
	session.cancel = func() {
		cancel()
		allocCancel()
	}

	if bm.config.DialogPolicy != "" {
		supervisor := NewCDPSupervisor(session.cdpURL, session.sessionID, bm.config.DialogPolicy, bm.config.DialogTimeout)
		if err := supervisor.Start(ctx); err == nil {
			session.supervisor = supervisor
		}
	}

	return nil
}

func (bm *BrowserManager) createAgentBrowserSession(ctx context.Context, session *BrowserSession) error {
	if bm.agentBrowserMgr == nil {
		bm.agentBrowserMgr = NewAgentBrowserManager()
	}

	abSession, err := bm.agentBrowserMgr.EnsureSession(ctx, session.sessionID)
	if err != nil {
		return fmt.Errorf("agent-browser session failed: %w", err)
	}

	session.backendType = BackendAgentBrowser
	session.cdpURL = abSession.CDPURL

	if abSession.CDPURL != "" {
		allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), abSession.CDPURL)
		browserCtx, cancel := chromedp.NewContext(allocCtx)
		session.ctx = browserCtx
		session.cancel = func() {
			cancel()
			allocCancel()
		}

		if bm.config.DialogPolicy != "" {
			supervisor := NewCDPSupervisor(abSession.CDPURL, session.sessionID, bm.config.DialogPolicy, bm.config.DialogTimeout)
			if err := supervisor.Start(ctx); err == nil {
				session.supervisor = supervisor
			}
		}
	}

	return nil
}

func (bm *BrowserManager) createLocalSession(ctx context.Context, session *BrowserSession) error {
	vpWidth := bm.config.ViewportWidth
	vpHeight := bm.config.ViewportHeight
	if vpWidth <= 0 {
		vpWidth = 1200 + rand.Intn(201)
	}
	if vpHeight <= 0 {
		vpHeight = 700 + rand.Intn(151)
	}

	chrome, err := FindChrome()
	if err != nil {
		return fmt.Errorf("chrome not found: %w - set CHROME_PATH env var", err)
	}

	opts := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(chrome.Path),
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("headless", fmt.Sprintf("%v", bm.config.Headless)),
		chromedp.WindowSize(vpWidth, vpHeight),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-features", "Translate,MediaRouter"),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-component-update", true),
		chromedp.Flag("hide-crash-restore-bubble", true),
		chromedp.Flag("password-store", "basic"),
		chromedp.Flag("remote-debugging-port", "0"),
	}

	if ua := bm.config.UserAgent; ua != "" {
		opts = append(opts, chromedp.Flag("user-agent", ua))
	} else if ua := os.Getenv("BROWSER_USER_AGENT"); ua != "" {
		opts = append(opts, chromedp.Flag("user-agent", ua))
	}

	acceptLang := bm.config.AcceptLanguage
	if acceptLang == "" {
		acceptLang = os.Getenv("BROWSER_ACCEPT_LANGUAGE")
	}
	if acceptLang == "" {
		acceptLang = "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7"
	}
	opts = append(opts, chromedp.Flag("accept-lang", acceptLang))

	if proxyURL := bm.config.ProxyURL; proxyURL != "" {
		opts = append(opts, chromedp.Flag("proxy-server", proxyURL))
		opts = append(opts, chromedp.Flag("proxy-bypass-list", "localhost;127.0.0.1;[::1]"))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	browserCtx, cancel := chromedp.NewContext(allocCtx)

	if err := chromedp.Run(browserCtx); err != nil {
		cancel()
		allocCancel()
		return fmt.Errorf("failed to start local browser (path=%s): %w", chrome.Path, err)
	}

	session.ctx = browserCtx
	session.cancel = func() {
		cancel()
		allocCancel()
	}
	session.backendType = BackendLocal

	return nil
}

func (bm *BrowserManager) CloseSession(sessionID string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if session, ok := bm.sessions[sessionID]; ok {
		if session.supervisor != nil {
			session.supervisor.Stop()
		}

		if session.recorder != nil && session.recorder.IsRecording() {
			session.recorder.StopRecording()
		}

		if session.cloudProvider != nil && session.cloudSessionID != "" {
			session.cloudProvider.CloseSession(session.cloudSessionID)
		}

		if session.camofoxClient != nil {
			session.camofoxClient.CloseTab(sessionID)
		}

		if session.lightpandaProc != nil {
			bm.lightpandaMgr.StopProcess(sessionID)
		}

		if session.backendType == BackendAgentBrowser && bm.agentBrowserMgr != nil {
			bm.agentBrowserMgr.StopSession(sessionID)
		}

		if session.cancel != nil {
			session.cancel()
		}

		delete(bm.sessions, sessionID)
		if bm.activeSession == sessionID {
			bm.activeSession = ""
		}
	}
}

func (bm *BrowserManager) CleanupInactiveSessions(timeout time.Duration) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	now := time.Now()
	for id, session := range bm.sessions {
		if now.Sub(session.lastActivity) > timeout {
			if session.supervisor != nil {
				session.supervisor.Stop()
			}

			if session.recorder != nil && session.recorder.IsRecording() {
				session.recorder.StopRecording()
			}

			if session.cloudProvider != nil && session.cloudSessionID != "" {
				session.cloudProvider.EmergencyCleanup(session.cloudSessionID)
			}

			if session.camofoxClient != nil {
				session.camofoxClient.CloseTab(id)
			}

			if session.lightpandaProc != nil {
				bm.lightpandaMgr.StopProcess(id)
			}

			if session.backendType == BackendAgentBrowser && bm.agentBrowserMgr != nil {
				bm.agentBrowserMgr.StopSession(id)
			}

			if session.cancel != nil {
				session.cancel()
			}

			delete(bm.sessions, id)
			if bm.activeSession == id {
				bm.activeSession = ""
			}
		}
	}
}

func (bm *BrowserManager) CloseAll() {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	for _, session := range bm.sessions {
		if session.supervisor != nil {
			session.supervisor.Stop()
		}

		if session.recorder != nil && session.recorder.IsRecording() {
			session.recorder.StopRecording()
		}

		if session.cloudProvider != nil && session.cloudSessionID != "" {
			session.cloudProvider.EmergencyCleanup(session.cloudSessionID)
		}

		if session.camofoxClient != nil {
			session.camofoxClient.CloseTab(session.sessionID)
		}

		if session.lightpandaProc != nil {
			bm.lightpandaMgr.StopProcess(session.sessionID)
		}

		if session.backendType == BackendAgentBrowser && bm.agentBrowserMgr != nil {
			bm.agentBrowserMgr.StopSession(session.sessionID)
		}

		if session.cancel != nil {
			session.cancel()
		}
	}

	if bm.agentBrowserMgr != nil {
		bm.agentBrowserMgr.StopAll()
	}

	bm.sessions = make(map[string]*BrowserSession)
	bm.activeSession = ""
}
