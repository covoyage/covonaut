package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

type DialogPolicy string

const (
	DialogMustRespond DialogPolicy = "must_respond"
	DialogAutoDismiss DialogPolicy = "auto_dismiss"
	DialogAutoAccept  DialogPolicy = "auto_accept"
)

type DialogInfo struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Default string `json:"default"`
}

type FrameInfo struct {
	ID       string       `json:"id"`
	URL      string       `json:"url"`
	Name     string       `json:"name"`
	ParentID string       `json:"parent_id"`
	Children []*FrameInfo `json:"children,omitempty"`
}

type CDPSupervisor struct {
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	cdpURL        string
	taskID        string
	dialogPolicy  DialogPolicy
	dialogTimeout time.Duration

	pendingDialogs map[string]*DialogInfo
	frameTree      []*FrameInfo
	maxFrames      int
	maxDepth       int

	reconnectBackoff time.Duration
	maxReconnects    int
	reconnectCount   int

	listeners []CDPEventListener
}

type CDPEvent struct {
	Type string
	Data any
}

type CDPEventListener func(event CDPEvent)

func NewCDPSupervisor(cdpURL string, taskID string, dialogPolicy DialogPolicy, dialogTimeout time.Duration) *CDPSupervisor {
	if dialogTimeout <= 0 {
		dialogTimeout = 300 * time.Second
	}
	return &CDPSupervisor{
		cdpURL:           cdpURL,
		taskID:           taskID,
		dialogPolicy:     dialogPolicy,
		dialogTimeout:    dialogTimeout,
		pendingDialogs:   make(map[string]*DialogInfo),
		maxFrames:        30,
		maxDepth:         2,
		reconnectBackoff: 2 * time.Second,
		maxReconnects:    5,
	}
}

func (s *CDPSupervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ctx != nil {
		return nil
	}

	s.ctx, s.cancel = context.WithCancel(ctx)

	go s.runSupervisor()

	return nil
}

func (s *CDPSupervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}
	s.ctx = nil
	s.cancel = nil
}

func (s *CDPSupervisor) runSupervisor() {
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		if err := s.connectAndListen(); err != nil {
			s.mu.RLock()
			count := s.reconnectCount
			s.mu.RUnlock()

			if count >= s.maxReconnects {
				return
			}

			s.mu.Lock()
			s.reconnectCount++
			s.mu.Unlock()

			select {
			case <-s.ctx.Done():
				return
			case <-time.After(s.reconnectBackoff):
				s.mu.Lock()
				s.reconnectBackoff *= 2
				if s.reconnectBackoff > 30*time.Second {
					s.reconnectBackoff = 30 * time.Second
				}
				s.mu.Unlock()
			}
		}
	}
}

func (s *CDPSupervisor) connectAndListen() error {
	s.mu.RLock()
	parentCtx := s.ctx
	cdpURL := s.cdpURL
	s.mu.RUnlock()
	if parentCtx == nil {
		return fmt.Errorf("supervisor context is not initialized")
	}

	var allocCtx context.Context
	var allocCancel context.CancelFunc
	if cdpURL != "" {
		allocCtx, allocCancel = chromedp.NewRemoteAllocator(parentCtx, cdpURL)
	} else {
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.NoSandbox,
			chromedp.DisableGPU,
		)
		allocCtx, allocCancel = chromedp.NewExecAllocator(parentCtx, opts...)
	}
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	if err := chromedp.Run(browserCtx); err != nil {
		return fmt.Errorf("browser start failed: %w", err)
	}

	s.mu.Lock()
	s.reconnectCount = 0
	s.reconnectBackoff = 2 * time.Second
	s.mu.Unlock()

	go s.listenForDialogs(browserCtx)
	go s.trackFrameTree(browserCtx)

	select {
	case <-parentCtx.Done():
		return parentCtx.Err()
	case <-browserCtx.Done():
		return browserCtx.Err()
	}
}

func (s *CDPSupervisor) listenForDialogs(ctx context.Context) {
	chromedp.ListenTarget(ctx, func(ev any) {
		switch e := ev.(type) {
		case *page.EventJavascriptDialogOpening:
			dialogID := fmt.Sprintf("dlg_%d", time.Now().UnixNano())
			s.mu.Lock()
			s.pendingDialogs[dialogID] = &DialogInfo{
				Type:    string(e.Type),
				Message: e.Message,
				Default: e.DefaultPrompt,
			}
			s.mu.Unlock()

			s.notifyListeners(CDPEvent{Type: "dialog_open", Data: e})

			switch s.dialogPolicy {
			case DialogAutoDismiss:
				s.HandleDialog(dialogID, false, "")
			case DialogAutoAccept:
				s.HandleDialog(dialogID, true, "")
			}

		case *page.EventJavascriptDialogClosed:
			s.mu.Lock()
			for id := range s.pendingDialogs {
				delete(s.pendingDialogs, id)
				break
			}
			s.mu.Unlock()

			s.notifyListeners(CDPEvent{Type: "dialog_closed", Data: e})
		}
	})
}

func (s *CDPSupervisor) trackFrameTree(ctx context.Context) {
	chromedp.ListenTarget(ctx, func(ev any) {
		switch ev.(type) {
		case *target.EventAttachedToTarget:
			s.mu.Lock()
			s.updateFrameTree(ctx)
			s.mu.Unlock()
		case *target.EventDetachedFromTarget:
			s.mu.Lock()
			s.updateFrameTree(ctx)
			s.mu.Unlock()
		}
	})
}

func (s *CDPSupervisor) updateFrameTree(ctx context.Context) {
	var tree *page.FrameTree
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		tree, err = page.GetFrameTree().Do(ctx)
		return err
	})); err != nil {
		return
	}

	s.frameTree = convertFrameTree(tree, 0, s.maxDepth)
	if len(s.frameTree) > s.maxFrames {
		s.frameTree = s.frameTree[:s.maxFrames]
	}
}

func convertFrameTree(tree *page.FrameTree, depth int, maxDepth int) []*FrameInfo {
	if tree == nil || depth > maxDepth {
		return nil
	}

	frame := &FrameInfo{
		ID:   tree.Frame.ID.String(),
		URL:  tree.Frame.URL,
		Name: tree.Frame.Name,
	}

	var children []*FrameInfo
	for _, child := range tree.ChildFrames {
		children = append(children, convertFrameTree(child, depth+1, maxDepth)...)
	}
	if len(children) > 0 {
		frame.Children = children
	}

	return []*FrameInfo{frame}
}

func (s *CDPSupervisor) GetPendingDialogs() map[string]*DialogInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]*DialogInfo)
	for k, v := range s.pendingDialogs {
		result[k] = v
	}
	return result
}

func (s *CDPSupervisor) HandleDialog(dialogID string, accept bool, promptText string) error {
	s.mu.RLock()
	ctx := s.ctx
	s.mu.RUnlock()

	if ctx == nil {
		return fmt.Errorf("supervisor not connected")
	}

	return chromedp.Run(ctx,
		page.HandleJavaScriptDialog(accept).WithPromptText(promptText),
	)
}

func (s *CDPSupervisor) EvaluateJS(expression string, targetFrameID string) (string, error) {
	s.mu.RLock()
	ctx := s.ctx
	s.mu.RUnlock()

	if ctx == nil {
		return "", fmt.Errorf("supervisor not connected")
	}

	var result string
	var err error

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(expression, &result),
	)

	if err != nil {
		return "", fmt.Errorf("JS evaluation failed: %w", err)
	}

	return result, nil
}

func (s *CDPSupervisor) GetFrameTree() []*FrameInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*FrameInfo, len(s.frameTree))
	copy(result, s.frameTree)
	return result
}

func (s *CDPSupervisor) IsTruncated() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.frameTree) >= s.maxFrames
}

func (s *CDPSupervisor) AddListener(listener CDPEventListener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, listener)
}

func (s *CDPSupervisor) notifyListeners(event CDPEvent) {
	s.mu.RLock()
	listeners := make([]CDPEventListener, len(s.listeners))
	copy(listeners, s.listeners)
	s.mu.RUnlock()

	for _, l := range listeners {
		l(event)
	}
}

func (s *CDPSupervisor) InjectDialogBridge() error {
	s.mu.RLock()
	ctx := s.ctx
	s.mu.RUnlock()

	if ctx == nil {
		return fmt.Errorf("supervisor not connected")
	}

	bridgeScript := `
(function() {
	const originalAlert = window.alert;
	const originalConfirm = window.confirm;
	const originalPrompt = window.prompt;

	window.alert = function(msg) {
		const xhr = new XMLHttpRequest();
		xhr.open('POST', 'http://__dialog_bridge__/alert', false);
		xhr.setRequestHeader('Content-Type', 'application/json');
		xhr.send(JSON.stringify({message: msg}));
		const resp = JSON.parse(xhr.responseText);
		return resp.accepted;
	};

	window.confirm = function(msg) {
		const xhr = new XMLHttpRequest();
		xhr.open('POST', 'http://__dialog_bridge__/confirm', false);
		xhr.setRequestHeader('Content-Type', 'application/json');
		xhr.send(JSON.stringify({message: msg}));
		const resp = JSON.parse(xhr.responseText);
		return resp.accepted;
	};

	window.prompt = function(msg, def) {
		const xhr = new XMLHttpRequest();
		xhr.open('POST', 'http://__dialog_bridge__/prompt', false);
		xhr.setRequestHeader('Content-Type', 'application/json');
		xhr.send(JSON.stringify({message: msg, default: def}));
		const resp = JSON.parse(xhr.responseText);
		return resp.value;
	};
})();
`

	return chromedp.Run(ctx, chromedp.Evaluate(bridgeScript, nil))
}

type DialogBridgeHandler struct {
	mu       sync.Mutex
	dialogs  []map[string]string
	policy   DialogPolicy
	timeout  time.Duration
	deadline time.Time
}

func NewDialogBridgeHandler(policy DialogPolicy, timeout time.Duration) *DialogBridgeHandler {
	return &DialogBridgeHandler{
		policy:   policy,
		timeout:  timeout,
		deadline: time.Now().Add(timeout),
	}
}

func (h *DialogBridgeHandler) HandleDialog(dialogType string, message string, defaultValue string) map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()

	if time.Now().After(h.deadline) {
		return map[string]string{"accepted": "false", "value": ""}
	}

	h.dialogs = append(h.dialogs, map[string]string{
		"type":    dialogType,
		"message": message,
		"default": defaultValue,
	})

	switch h.policy {
	case DialogAutoDismiss:
		return map[string]string{"accepted": "false", "value": ""}
	case DialogAutoAccept:
		return map[string]string{"accepted": "true", "value": defaultValue}
	default:
		return map[string]string{"accepted": "pending", "value": ""}
	}
}

func (h *DialogBridgeHandler) GetDialogs() []map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := make([]map[string]string, len(h.dialogs))
	copy(result, h.dialogs)
	return result
}

func (h *DialogBridgeHandler) RespondToPending(accepted bool, value string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.dialogs) > 0 {
		last := h.dialogs[len(h.dialogs)-1]
		last["accepted"] = fmt.Sprintf("%t", accepted)
		last["value"] = value
	}
}

func formatDialogs(dialogs map[string]*DialogInfo) string {
	if len(dialogs) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Pending Dialogs\n")
	for id, d := range dialogs {
		sb.WriteString(fmt.Sprintf("- [%s] %s: \"%s\"", id, d.Type, d.Message))
		if d.Default != "" {
			sb.WriteString(fmt.Sprintf(" (default: %s)", d.Default))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func formatFrameTree(frames []*FrameInfo, truncated bool) string {
	if len(frames) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Frame Tree\n")
	for _, f := range frames {
		sb.WriteString(fmt.Sprintf("- Frame %s: %s", f.ID, f.URL))
		if f.Name != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", f.Name))
		}
		sb.WriteString("\n")
	}
	if truncated {
		sb.WriteString("[... frame tree truncated]\n")
	}
	return sb.String()
}

type SupervisorRegistry struct {
	mu          sync.RWMutex
	supervisors map[string]*CDPSupervisor
}

var globalSupervisorRegistry = &SupervisorRegistry{
	supervisors: make(map[string]*CDPSupervisor),
}

func GetSupervisorRegistry() *SupervisorRegistry {
	return globalSupervisorRegistry
}

func (r *SupervisorRegistry) GetOrStart(taskID string, cdpURL string, dialogPolicy DialogPolicy, dialogTimeout time.Duration) (*CDPSupervisor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.supervisors[taskID]; ok {
		return existing, nil
	}

	supervisor := NewCDPSupervisor(cdpURL, taskID, dialogPolicy, dialogTimeout)
	r.supervisors[taskID] = supervisor

	ctx := context.Background()
	if err := supervisor.Start(ctx); err != nil {
		delete(r.supervisors, taskID)
		return nil, err
	}

	return supervisor, nil
}

func (r *SupervisorRegistry) Get(taskID string) (*CDPSupervisor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.supervisors[taskID]
	return s, ok
}

func (r *SupervisorRegistry) Stop(taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s, ok := r.supervisors[taskID]; ok {
		s.Stop()
		delete(r.supervisors, taskID)
	}
}

func (r *SupervisorRegistry) StopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for id, s := range r.supervisors {
		s.Stop()
		delete(r.supervisors, id)
	}
}

type CloudBrowserProvider interface {
	ProviderName() string
	IsConfigured() bool
	CreateSession(taskID string) (map[string]string, error)
	CloseSession(sessionID string) error
	EmergencyCleanup(sessionID string)
}

type CloudSessionInfo struct {
	SessionName string
	SessionID   string
	CDPURL      string
	Features    map[string]bool
}

type BrowserBackendType string

const (
	BackendLocal       BrowserBackendType = "local"
	BackendCDP         BrowserBackendType = "cdp"
	BackendCamofox     BrowserBackendType = "camofox"
	BackendBrowserbase BrowserBackendType = "browserbase"
	BackendBrowserUse  BrowserBackendType = "browser_use"
	BackendFirecrawl   BrowserBackendType = "firecrawl"
	BackendLightpanda   BrowserBackendType = "lightpanda"
	BackendAgentBrowser BrowserBackendType = "agent_browser"
)

type BrowserConfig struct {
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
	InactivityTimeout   time.Duration
	UserAgent           string
	AcceptLanguage      string
	ProxyURL            string
	ViewportWidth       int
	ViewportHeight      int
	AgentBrowserEnabled bool
}

func (c *BrowserConfig) defaults() {
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
}

func DetectBackend(cfg *BrowserConfig) BrowserBackendType {
	if cfg.CDPURL != "" {
		return BackendCDP
	}
	if cfg.CamofoxURL != "" {
		return BackendCamofox
	}
	if cfg.AgentBrowserEnabled {
		return BackendAgentBrowser
	}
	if cfg.Engine == "lightpanda" {
		return BackendLightpanda
	}
	switch cfg.CloudProvider {
	case "browserbase":
		return BackendBrowserbase
	case "browser_use":
		return BackendBrowserUse
	case "firecrawl":
		return BackendFirecrawl
	case "local":
		return BackendLocal
	default:
		return BackendLocal
	}
}
