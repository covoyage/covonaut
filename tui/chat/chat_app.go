package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/tui/component"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
)

type AppHost interface {
	Start() error
	Stop() error
	Done() <-chan struct{}

	AddChild(c core.Component)
	Focus(c core.Component)
	RequestRender()

	PushOverlay(ov OverlayRef)
	RemoveOverlay(ov OverlayRef) bool

	TerminalSize() (cols, rows int64)

	// EnableMouse / DisableMouse toggle SGR mouse reporting at runtime.
	// Used to disable mouse when the editor is focused so the terminal's
	// native right-click menu can appear.
	EnableMouse(mode string)
	DisableMouse()
}

type OverlayRef interface {
	OverlayContent() core.Component
	SetOverlayFocus(bool)
	SetOverlayDimBackground(bool)
	OverlayWantsFocus() bool
	OverlayDimBackground() bool
	OverlayAnchor() int
	OverlayPercentX() int
	OverlayPercentY() int
	OverlayWidthPct() int
	OverlayHeightPct() int
}

type ChatAppConfig struct {
	Title string

	KittyKeyboardMode     string
	KittyKeyboardFlags    int64
	DisableBracketedPaste bool
	AltScreen             bool
	MouseMode             string

	EditorMinRows int64
	EditorMaxRows int64
	EditorPrompt  string

	ShowTimings bool
	ShowTurns   bool

	// ReasoningRenderer controls how thinking segments are displayed in the
	// chat history. Pass nil (default) to hide reasoning; pass a
	// *DefaultReasoningRenderer to restore the legacy Show/Mode policy; pass
	// a custom implementation for full control (sidebar, overlay, etc.).
	ReasoningRenderer ReasoningRenderer

	Theme *ChatHistoryTheme

	Providers []core.AutocompleteProvider

	// Context is the cancellation root passed to OnSubmit. When nil,
	// context.Background() is used. Callers should wire the TUI's lifecycle
	// context here so in-flight submissions are cancelled on Stop.
	Context context.Context

	OnSubmit     func(ctx context.Context, input string)
	OnQuit       func()
	OnInterrupt  func()
	OnImagePaste func() // called when an image paste is detected (clipboard image, empty text)

	Host AppHost
}

type chatModel struct {
	StreamID    string
	ActiveTools map[string]time.Time
	Running     bool
}

type ChatApp struct {
	cfg ChatAppConfig

	host      AppHost
	history   *ChatHistory
	editor    *component.Editor
	loader    *component.Loader
	layout    *chatLayout
	header    *component.TruncatedText
	statusBar *component.StatusBar
	ac        *component.Autocomplete
	km        *terminal.KeybindingsManager

	mu    sync.Mutex
	model chatModel

	helpOverlay OverlayRef

	// SuppressAutoRetry suppresses auto-retry messages from being printed to
	// the chat history. When true, retry events are silently dropped instead of
	// showing "⚠ retry N/M in D". Used by covo-agent to buffer retry messages
	// and only flush them on final failure (Hermes-style buffered retry).
	SuppressAutoRetry bool

	skipRefresh bool // suppress autocomplete re-activation after applying a suggestion
}

func NewChatApp(cfg ChatAppConfig) *ChatApp {
	return newChatApp(cfg)
}

func NewChatAppWithHost(cfg ChatAppConfig, host AppHost) *ChatApp {
	cfg.Host = host
	return newChatApp(cfg)
}

func newChatApp(cfg ChatAppConfig) *ChatApp {
	if cfg.EditorMinRows <= 0 {
		cfg.EditorMinRows = 1
	}
	if cfg.EditorMaxRows <= 0 {
		cfg.EditorMaxRows = 8
	}
	if cfg.EditorPrompt == "" {
		cfg.EditorPrompt = "> "
	}

	km := terminal.NewKeybindingsManager(terminal.DefaultKeybindings())

	history := NewChatHistory()
	if cfg.Theme != nil {
		history.SetTheme(*cfg.Theme)
	}
	if cfg.ReasoningRenderer != nil {
		history.SetReasoningRenderer(cfg.ReasoningRenderer)
	}

	editor := component.NewEditor(km)
	editor.SetMinRows(cfg.EditorMinRows)
	editor.SetMaxRows(cfg.EditorMaxRows)
	editor.SetPrompt(cfg.EditorPrompt, strings.Repeat(" ", len(cfg.EditorPrompt)))
	editor.SetFocusIndicator("")
	editor.SetPlaceholder("Type a message...")
	editor.SetPlaceholderFn(func(s string) string { return theme.CurrentPalette().Dim.Render(s) })

	loader := component.NewLoader(func() {}, theme.CurrentPalette().Dim.Render("thinking..."))

	statusBar := component.NewStatusBar()
	if cfg.Title != "" {
		statusBar.SetMode(cfg.Title)
	}

	chatApp := &ChatApp{
		cfg:       cfg,
		host:      cfg.Host,
		history:   history,
		editor:    editor,
		loader:    loader,
		statusBar: statusBar,
		km:        km,
		model:     chatModel{ActiveTools: make(map[string]time.Time)},
	}

	var header *component.TruncatedText
	if cfg.Title != "" {
		header = component.NewTruncatedText(theme.CurrentPalette().User.Render(cfg.Title))
	}
	chatApp.header = header

	if len(cfg.Providers) > 0 {
		chatApp.ac = component.NewAutocomplete(cfg.Providers...)
		chatApp.ac.OnApply(func(newValue string, _ int64, _ core.Suggestion) {
			chatApp.skipRefresh = true
			chatApp.editor.SetValue(newValue)
			// Force a refresh only when switching to a file/folder browser so
			// that cascading providers activate immediately.
			if strings.HasPrefix(newValue, "@file:") || strings.HasPrefix(newValue, "@folder:") {
				chatApp.ac.Refresh(newValue, int64(len(newValue)))
			}
			chatApp.host.Focus(chatApp.editor)
			chatApp.host.RequestRender()
		})
		chatApp.ac.OnDismiss(func() {
			// File browser: ESC navigates up one directory level instead of
			// dismissing outright. At the root, dismiss normally.
			value := chatApp.editor.GetValue()
			if (strings.HasPrefix(value, "@file:") || strings.HasPrefix(value, "@folder:")) &&
				len(value) > len("@file:") {
				// Remove the last path segment (file or directory).
				newValue := popLastPathSegment(value)
				chatApp.editor.SetValue(newValue)
				chatApp.ac.Refresh(newValue, int64(len(newValue)))
			}
			chatApp.host.RequestRender()
		})
	}

	layout := &chatLayout{
		host:      chatApp,
		app:       chatApp,
		history:   history,
		editor:    editor,
		loader:    loader,
		statusBar: statusBar,
		ac:        chatApp.ac,
	}
	if header != nil {
		layout.header = header
	}
	chatApp.layout = layout

	editor.OnChange(func(value string) {
		if chatApp.ac != nil {
			if chatApp.skipRefresh {
				chatApp.skipRefresh = false
			} else {
				chatApp.ac.Refresh(value, int64(len(value)))
			}
			chatApp.host.RequestRender()
		}
	})
	editor.OnSubmit(func(value string) {
		chatApp.onEditorSubmit(value)
	})
	editor.OnCancel(func() {
		if cfg.OnQuit != nil {
			cfg.OnQuit()
		}
		chatApp.Stop()
	})

	history.SetOnCopy(func(text string) {
		go func() {
			if err := CopyToClipboard(text); err != nil {
				chatApp.PrintError(fmt.Errorf("clipboard: %w", err))
			}
		}()
	})

	return chatApp
}

func (a *ChatApp) SetHost(host AppHost) {
	a.host = host
	a.loader = component.NewLoader(host.RequestRender, theme.CurrentPalette().Dim.Render("thinking..."))
	a.layout.loader = a.loader
	a.history.SetOnInvalidate(host.RequestRender)
}

func (a *ChatApp) Host() AppHost { return a.host }

func (a *ChatApp) History() *ChatHistory { return a.history }

func (a *ChatApp) Editor() *component.Editor { return a.editor }

func (a *ChatApp) Loader() *component.Loader { return a.loader }

func (a *ChatApp) Keybindings() *terminal.KeybindingsManager { return a.km }

func (a *ChatApp) StatusBar() *component.StatusBar { return a.statusBar }

// UpdateStatusBar updates the status bar title after provider/model/mode switches.
func (a *ChatApp) UpdateStatusBar(provider, model, mode string) {
	if a.statusBar == nil {
		return
	}
	a.statusBar.SetMode(fmt.Sprintf("covo-agent · provider=%s model=%s mode=%s", provider, model, mode))
}

func (a *ChatApp) SetFooter(f core.Component) {
	a.layout.footer = f
	if a.host != nil {
		a.host.RequestRender()
	}
}

func (a *ChatApp) Footer() core.Component {
	return a.layout.footer
}

func (a *ChatApp) Start() error {
	a.host.AddChild(a.layout)
	if err := a.host.Start(); err != nil {
		return err
	}
	a.host.Focus(a.editor)
	return nil
}

func (a *ChatApp) Stop() error { return a.host.Stop() }

func (a *ChatApp) Done() <-chan struct{} { return a.host.Done() }

func (a *ChatApp) snapshot() chatModel {
	a.mu.Lock()
	defer a.mu.Unlock()
	tools := make(map[string]time.Time, len(a.model.ActiveTools))
	for k, v := range a.model.ActiveTools {
		tools[k] = v
	}
	return chatModel{
		StreamID:    a.model.StreamID,
		ActiveTools: tools,
		Running:     a.model.Running,
	}
}

func (a *ChatApp) isRunning() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.model.Running
}

func (a *ChatApp) Subscribe(sub EventSubscriber) {
	sub.On(ChatEventAgentStart, a.onAgentStart)
	sub.On(ChatEventMessageDelta, a.onMessageDelta)
	sub.On(ChatEventToolCallStart, a.onToolStart)
	sub.On(ChatEventToolCallEnd, a.onToolEnd)
	sub.On(ChatEventHandoffStart, a.onHandoffStart)
	sub.On(ChatEventHandoffEnd, a.onHandoffEnd)
	sub.On(ChatEventTurnStart, a.onTurnStart)
	sub.On(ChatEventTurnEnd, a.onTurnEnd)
	sub.On(ChatEventCompactionStart, a.onCompactionStart)
	sub.On(ChatEventCompactionEnd, a.onCompactionEnd)
	sub.On(ChatEventAutoRetry, a.onAutoRetry)
	sub.On(ChatEventAgentError, a.onAgentError)
	sub.On(ChatEventAgentEnd, a.onAgentEnd)
}

func (a *ChatApp) PrintSystem(msg string) {
	a.history.Append(ChatMessage{Role: RoleSystem, Text: msg})
}

func (a *ChatApp) PrintError(err error) {
	if err == nil {
		return
	}
	a.history.Append(ChatMessage{Role: RoleError, Text: err.Error()})
}

func (a *ChatApp) PrintUser(input string) {
	a.history.Append(ChatMessage{Role: RoleUser, Text: input})
}

func (a *ChatApp) Busy(message string) {
	if message != "" {
		a.loader.SetMessage(theme.CurrentPalette().Dim.Render(message))
	}
	a.loader.Start()
	a.mu.Lock()
	a.model.Running = true
	a.mu.Unlock()
	if a.statusBar != nil {
		a.statusBar.Busy()
	}
	a.editor.SetPlaceholder("Ctrl+C to interrupt")
	a.host.RequestRender()
}

func (a *ChatApp) Idle() {
	a.loader.Stop()
	a.mu.Lock()
	a.model.Running = false
	a.mu.Unlock()
	if a.statusBar != nil {
		a.statusBar.Idle()
	}
	a.editor.SetPlaceholder("")
	a.host.RequestRender()
}

func (a *ChatApp) PrintStatus(message string) {
	a.loader.SetMessage(theme.CurrentPalette().Dim.Render(message))
	a.host.RequestRender()
}

func (a *ChatApp) ToggleKeyHelp() OverlayRef {
	a.mu.Lock()
	if a.helpOverlay != nil {
		// Close path: capture the overlay under the lock, then call host
		// methods outside the lock. Calling host.RemoveOverlay/PushOverlay
		// under a.mu would acquire host.mu / TUI.mu; if any other path took
		// those locks first and then ChatApp.mu, we'd deadlock.
		ov := a.helpOverlay
		a.helpOverlay = nil
		editor := a.editor
		a.mu.Unlock()
		a.host.RemoveOverlay(ov)
		a.host.Focus(editor)
		return nil
	}
	help := component.NewKeyHelp(a.km)
	help.SetTitle("Keybindings — Esc to close")
	ov := &overlayHandle{
		content:       help,
		focus:         true,
		dimBackground: true,
		widthPct:      70,
		heightPct:     70,
	}
	a.helpOverlay = ov
	a.mu.Unlock()
	a.host.PushOverlay(ov)
	return ov
}

func (a *ChatApp) CloseKeyHelp() {
	a.mu.Lock()
	ov := a.helpOverlay
	a.helpOverlay = nil
	a.mu.Unlock()
	if ov != nil {
		a.host.RemoveOverlay(ov)
		a.host.Focus(a.editor)
	}
}

type overlayHandle struct {
	content       core.Component
	focus         bool
	dimBackground bool
	anchor        int
	percentX      int
	percentY      int
	widthPct      int
	heightPct     int
}

func (o *overlayHandle) OverlayContent() core.Component { return o.content }
func (o *overlayHandle) SetOverlayFocus(v bool)         { o.focus = v }
func (o *overlayHandle) SetOverlayDimBackground(v bool) { o.dimBackground = v }
func (o *overlayHandle) OverlayWantsFocus() bool        { return o.focus }
func (o *overlayHandle) OverlayDimBackground() bool     { return o.dimBackground }
func (o *overlayHandle) OverlayAnchor() int             { return o.anchor }
func (o *overlayHandle) OverlayPercentX() int           { return o.percentX }
func (o *overlayHandle) OverlayPercentY() int           { return o.percentY }
func (o *overlayHandle) OverlayWidthPct() int           { return o.widthPct }
func (o *overlayHandle) OverlayHeightPct() int          { return o.heightPct }

func (a *ChatApp) onEditorSubmit(value string) {
	a.mu.Lock()
	if a.ac != nil && a.ac.Active() {
		// Autocomplete is active — let chatLayout forward the key to it.
		// Don't submit or forward here to avoid double-processing.
		a.mu.Unlock()
		return
	}
	// Hide autocomplete under the same lock that checked Active() — without
	// this, the unlock window between the check and Hide() could let the ac
	// state drift (the previous code re-locked and re-checked a.ac != nil,
	// racing with concurrent activation).
	if a.ac != nil {
		a.ac.Hide()
	}
	onSubmit := a.cfg.OnSubmit
	ctx := a.cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Unlock()

	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	// PrintUser / PushInputHistory / SetValue operate on history and editor
	// components, which own their own internal locks — they do not touch
	// ChatApp.model, so holding ChatApp.mu here is unnecessary and would
	// serialise with the event loop for no benefit.
	a.PrintUser(trimmed)
	a.editor.PushInputHistory(trimmed)
	a.editor.SetValue("")
	if onSubmit != nil {
		onSubmit(ctx, trimmed)
	}
}

func (a *ChatApp) onAgentStart(e ChatEvent) {
	if _, ok := e.(AgentStartChatEvent); !ok {
		return
	}
	a.mu.Lock()
	a.model.StreamID = ""
	a.mu.Unlock()
	a.Busy("thinking...")
}

func (a *ChatApp) onMessageDelta(e ChatEvent) {
	d, ok := e.(MessageDeltaChatEvent)
	if !ok {
		return
	}
	// Read-modify-write StreamID under a single critical section. The
	// previous code released the lock between reading StreamID and writing
	// the new one, so two concurrent deltas could both read the same old id
	// and both append to the same baseline — corrupting the stream.
	a.mu.Lock()
	defer a.mu.Unlock()
	id := a.model.StreamID
	newID := a.history.AppendDelta(id, d.Delta)
	if newID != id {
		a.model.StreamID = newID
	}
}

func (a *ChatApp) onToolStart(e ChatEvent) {
	tc, ok := e.(ToolCallStartChatEvent)
	if !ok {
		return
	}
	a.mu.Lock()
	a.model.ActiveTools[tc.ToolCall.ID] = time.Now()
	a.finalizeStreamLocked()
	a.mu.Unlock()
	a.history.Append(ChatMessage{
		ID:   "tool-" + tc.ToolCall.ID,
		Role: RoleTool,
		Meta: tc.ToolCall.Name,
		Text: theme.CurrentPalette().Dim.Render("..."),
	})
}

func (a *ChatApp) onToolEnd(e ChatEvent) {
	tc, ok := e.(ToolCallEndChatEvent)
	if !ok {
		return
	}
	a.mu.Lock()
	delete(a.model.ActiveTools, tc.ToolCallID)
	a.mu.Unlock()

	status := theme.CurrentPalette().Success.Render(theme.SymbolCheck + " done")
	dur := time.Duration(0)
	if a.cfg.ShowTimings {
		dur = tc.Duration
	}
	if tc.Err != nil {
		errMsg := tc.Err.Error()
		if core.VisibleWidth(errMsg) > 180 {
			errMsg = core.TruncateToWidth(errMsg, 177, "...")
		}
		status = theme.CurrentPalette().Error.Render(theme.SymbolCross + " failed: " + errMsg)
	}
	toolID := "tool-" + tc.ToolCallID
	collapsed := len(status) > 120
	if !a.history.PatchMessage(toolID, func(m *ChatMessage) {
		m.Text = status
		m.Duration = dur
		m.Collapsed = collapsed
	}) {
		a.history.Append(ChatMessage{
			Role:      RoleTool,
			Meta:      tc.ToolName,
			Text:      status,
			Duration:  dur,
			Collapsed: collapsed,
		})
	}

	// Automatically show diff blocks for edit/file-writing tools (inline, collapsed).
	if tc.Err == nil && tc.Result != "" && isEditorTool(tc.ToolName) {
		filePath, diffText, added, removed, fileContent := extractToolDiff(tc.ToolName, tc.Result)
		if diffText != "" || filePath != "" {
			var parts []string
			if filePath != "" {
				switch {
				case (tc.ToolName == "write_file" || tc.ToolName == "write") && fileContent != "":
					// Write tool: show content preview.
					parts = append(parts, fmt.Sprintf("⌨  Wrote %d lines to %s", added, filePath))
					parts = append(parts, formatContentPreview(fileContent, added))
				default:
					summary := fmt.Sprintf("✏️ %s", filePath)
					if added > 0 || removed > 0 {
						if added > 0 {
							summary += " " + theme.CurrentPalette().Success.Render(fmt.Sprintf("+%d", added))
						}
						if removed > 0 {
							summary += " " + theme.CurrentPalette().Error.Render(fmt.Sprintf("-%d", removed))
						}
					}
					parts = append(parts, summary)
				}
			}
			if diffText != "" {
				parts = append(parts, "```diff")
				parts = append(parts, diffText)
				parts = append(parts, "```")
			}
			if len(parts) > 0 {
				a.history.Append(ChatMessage{
					Role:      RoleAssistant,
					Meta:      "diff",
					Text:      strings.Join(parts, "\n"),
					Collapsed: false,
				})
			}
		}
	}
}

var editorTools = map[string]bool{
	"edit_block":  true,
	"edit":        true,
	"write_file":  true,
	"write":       true,
	"apply_patch": true,
}

func isEditorTool(name string) bool {
	return editorTools[name]
}

func extractToolDiff(toolName, resultJSON string) (path, diff string, added, removed int64, content string) {
	var raw struct {
		Path     string `json:"path"`
		Diff     string `json:"diff"`
		Patch    string `json:"patch"`
		OldLines int64  `json:"old_lines"`
		NewLines int64  `json:"new_lines"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &raw); err != nil {
		return "", "", 0, 0, ""
	}

	path = raw.Path
	added = raw.NewLines
	removed = raw.OldLines
	content = raw.Content

	switch toolName {
	case "edit_block", "edit":
		// Prefer unified patch, fall back to full diff.
		diff = raw.Patch
		if diff == "" {
			diff = raw.Diff
		}
	case "apply_patch":
		diff = raw.Patch
		if diff == "" {
			diff = raw.Diff
		}
		if diff != "" {
			if added == 0 && removed == 0 {
				for _, line := range strings.Split(diff, "\n") {
					if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
						added++
					} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
						removed++
					}
				}
			}
		}
	case "write_file", "write":
		if content != "" {
			added = int64(strings.Count(content, "\n")) + 1
		}
		return path, "", added, removed, content
	}
	return path, diff, added, removed, content
}

func formatContentPreview(content string, totalLines int64) string {
	const previewLines = 6
	lines := strings.Split(content, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i >= previewLines {
			break
		}
		b.WriteString("  ")
		b.WriteString(line)
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	if int64(len(lines)) > previewLines {
		b.WriteString(fmt.Sprintf("\n  ... +%d lines", int64(len(lines))-previewLines))
	}
	return b.String()
}

func (a *ChatApp) onHandoffStart(e ChatEvent) {
	h, ok := e.(HandoffStartChatEvent)
	if !ok {
		return
	}
	a.mu.Lock()
	a.finalizeStreamLocked()
	a.mu.Unlock()
	a.history.Append(ChatMessage{
		Role: RoleSystem,
		Text: fmt.Sprintf("%s handoff %s %s %s (%s)",
			theme.SymbolArrow, h.SourceAgent, theme.SymbolArrow, h.TargetAgent, h.Mode),
	})
}

func (a *ChatApp) onHandoffEnd(e ChatEvent) {
	h, ok := e.(HandoffEndChatEvent)
	if !ok {
		return
	}
	if h.Err != nil {
		a.history.Append(ChatMessage{
			Role: RoleError,
			Text: fmt.Sprintf("%s handoff failed: %s", h.TargetAgent, h.Err.Error()),
		})
		return
	}
	meta := h.TargetAgent + " done"
	if a.cfg.ShowTimings {
		meta += fmt.Sprintf(" (%s)", h.Duration.Round(time.Millisecond))
	}
	a.history.Append(ChatMessage{Role: RoleSystem, Text: theme.SymbolCheck + " " + meta})
}

func (a *ChatApp) onTurnStart(e ChatEvent) {
	t, ok := e.(TurnStartChatEvent)
	if !ok {
		return
	}
	if a.cfg.ShowTurns && t.Turn > 1 {
		a.history.Append(ChatMessage{
			Role: RoleDivider,
			Text: fmt.Sprintf("turn %d", t.Turn),
		})
	}
}

func (a *ChatApp) onTurnEnd(e ChatEvent) {
	// Collapse consecutive tool messages now that the turn is complete.
	// (Token-usage display, if enabled, is owned by the StatusBar via its
	// own event subscription; this hook no longer forwards raw numbers to
	// a config callback. Consumers needing the numbers should subscribe to
	// ChatEventTurnEnd directly.)
	a.history.CollapseConsecutiveTools()
}

func (a *ChatApp) onCompactionStart(e ChatEvent) {
	ev, ok := e.(CompactionStartChatEvent)
	if !ok {
		return
	}
	a.Busy(fmt.Sprintf("compacting context (%d tokens)...", ev.TokensBefore))
}

func (a *ChatApp) onCompactionEnd(e ChatEvent) {
	ev, ok := e.(CompactionEndChatEvent)
	if !ok {
		return
	}
	a.history.Append(ChatMessage{
		Role: RoleSystem,
		Text: fmt.Sprintf("%s compacted %d %s %d tokens (-%d msgs, %s)",
			theme.SymbolCheck, ev.TokensBefore, theme.SymbolArrow, ev.TokensAfter,
			ev.MessagesCut, ev.Duration.Round(time.Millisecond)),
	})
}

func (a *ChatApp) onAutoRetry(e ChatEvent) {
	r, ok := e.(AutoRetryChatEvent)
	if !ok {
		return
	}
	if a.SuppressAutoRetry {
		return
	}
	a.history.Append(ChatMessage{
		Role: RoleSystem,
		Text: fmt.Sprintf("%s retry %d/%d in %s",
			theme.SymbolWarning, r.Attempt, r.MaxRetries, r.Delay.Round(time.Millisecond)),
	})
}

func (a *ChatApp) onAgentError(e ChatEvent) {
	ev, ok := e.(AgentErrorChatEvent)
	if !ok {
		return
	}
	a.mu.Lock()
	a.finalizeStreamLocked()
	a.mu.Unlock()
	a.Idle()
	a.PrintError(ev.Err)
}

func (a *ChatApp) onAgentEnd(e ChatEvent) {
	if _, ok := e.(AgentEndChatEvent); !ok {
		return
	}
	a.mu.Lock()
	a.finalizeStreamLocked()
	a.mu.Unlock()
	a.Idle()
}

func (a *ChatApp) finalizeStreamLocked() {
	id := a.model.StreamID
	a.model.StreamID = ""
	if id != "" {
		a.history.Finalize(id)
	}
}

func (a *ChatApp) TerminalSize() (cols, rows int64) {
	if a.host != nil {
		return a.host.TerminalSize()
	}
	return 80, 24
}

type layoutHost interface {
	TerminalSize() (cols, rows int64)
}

type chatLayout struct {
	host         layoutHost
	app          *ChatApp
	header       core.Component
	history      *ChatHistory
	loader       *component.Loader
	editor       core.Component
	statusBar    *component.StatusBar
	footer       core.Component
	ac           *component.Autocomplete
	lastRows     int64
	headerHeight int
	// editorTop is the absolute screen row of the editor's top border, as
	// computed by the most recent Render call. Used to translate MouseMsg
	// screen coordinates into the editor's own row space (see Update).
	editorTop int64
}

type textSelectionComponent interface {
	GetSelectedText() string
	ClearSelection()
}

func (l *chatLayout) Render(width int64) []string {
	var rows int64
	if l.host != nil {
		_, rows = l.host.TerminalSize()
	}
	if rows <= 0 {
		rows = l.lastRows
	}
	if rows <= 0 {
		rows = 24
	}

	var out []string
	var headerLines, loaderLines, editorLines, statusLines, footerLines, acLines []string

	if l.header != nil {
		headerLines = l.header.Render(width)
	}
	l.headerHeight = len(headerLines)
	editorLines = l.editor.Render(width)
	editorBorder := theme.CurrentPalette().Border.Render(strings.Repeat("─", int(width)))
	editorLines = append(append([]string{editorBorder}, editorLines...), editorBorder)
	if l.loader != nil && l.loader.IsRunning() {
		loaderLines = l.loader.Render(width)
	}
	if l.footer != nil {
		footerLines = l.footer.Render(width)
	}
	if l.statusBar != nil {
		statusLines = l.statusBar.Render(width)
	}
	if l.ac != nil && l.ac.Active() {
		acLines = l.ac.Render(width)
	}

	reserved := int64(len(headerLines) + len(editorLines) + len(loaderLines) + len(footerLines) + len(statusLines) + len(acLines))
	remaining := rows - reserved
	if remaining < 1 {
		remaining = 1
	}

	// Row order below is: header, history(remaining), ac, loader, editor(with
	// top/bottom border), footer, statusBar. editorTop marks where the
	// editor's top border row lands; the editor's own content starts one row
	// after that.
	l.editorTop = int64(len(headerLines)) + remaining + int64(len(acLines)) + int64(len(loaderLines))

	var historyLines []string
	if l.history != nil {
		l.history.SetMaxRowsDirect(remaining)
		historyLines = l.history.Render(width)
	}

	out = append(out, headerLines...)
	out = append(out, historyLines...)
	for int64(len(historyLines)) < remaining {
		out = append(out, strings.Repeat(" ", int(width)))
		historyLines = append(historyLines, " ")
	}
	out = append(out, acLines...)
	out = append(out, loaderLines...)
	out = append(out, editorLines...)
	out = append(out, footerLines...)
	out = append(out, statusLines...)
	return out
}

func (l *chatLayout) Invalidate() {}

func doCopy(l *chatLayout) {
	// Copy editor selection first
	if sel, ok := l.editor.(textSelectionComponent); ok {
		if text := sel.GetSelectedText(); text != "" {
			sel.ClearSelection()
			go func() {
				if err := CopyToClipboard(text); err != nil {
					l.app.PrintError(fmt.Errorf("clipboard: %w", err))
				}
			}()
			return
		}
	}
	// Copy history selection
	text := l.history.GetSelectedText()
	if text != "" {
		l.history.ClearSelection()
		go func() {
			if err := CopyToClipboard(text); err != nil {
				l.app.PrintError(fmt.Errorf("clipboard: %w", err))
			}
		}()
		return
	}
	// No selection — do nothing. Cmd/Ctrl+C only copies explicitly selected
	// content; falling back to the last assistant message would overwrite the
	// clipboard with content the user never asked to copy.
}

// hasSelection reports whether the editor or chat history currently has a
// non-empty text selection, without clearing it. Used to decide whether
// Ctrl/Cmd+C should copy instead of interrupting the running agent.
func hasSelection(l *chatLayout) bool {
	if sel, ok := l.editor.(textSelectionComponent); ok && sel.GetSelectedText() != "" {
		return true
	}
	return l.history.GetSelectedText() != ""
}

func isPrimaryShortcutMod(mods terminal.Modifier) bool {
	return mods&terminal.ModCtrl != 0 || mods&terminal.ModSuper != 0 || mods&terminal.ModMeta != 0
}

func isCopyShortcut(k terminal.Key) bool {
	name := strings.ToLower(k.Name)
	if name == "c" {
		return isPrimaryShortcutMod(k.Mods)
	}
	if name == "insert" {
		return k.Mods&terminal.ModCtrl != 0 || k.Mods&terminal.ModShift != 0
	}
	return false
}

func (l *chatLayout) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.WindowSizeMsg:
		l.lastRows = m.Height
		l.recalcMaxRows(m.Width, m.Height)
	case core.PasteMsg:
		// Image paste detection: when clipboard has an image, the terminal
		// sends empty/short text.  Let the caller hook pasteImageFn.
		if m.Text == "" || (len(m.Text) < 4 && m.Text == "\r") {
			if l.app.cfg.OnImagePaste != nil {
				l.app.cfg.OnImagePaste()
				return nil
			}
		}
	}
	if l.history != nil {
		switch m := msg.(type) {
		case core.MouseMsg:
			adjusted := m
			adjusted.Row -= int64(l.headerHeight)
			if adjusted.Row >= 0 {
				l.history.Update(adjusted)
			}
			// Also forward to the editor (row-adjusted for its own screen
			// position, skipping its top border row) so click-drag text
			// selection works inside the input box. Mouse mode itself stays
			// globally enabled — the editor bounds-checks and ignores events
			// outside its own rendered rows.
			//
			// Forwarded unconditionally (not gated on the adjusted row being
			// in range): once a drag starts, the mouse can easily drift onto
			// the editor's border row or outside it entirely before the
			// button is released. If MouseRelease were dropped here because
			// the row briefly fell out of range, the editor would be stuck
			// with selDragging=true forever, and every later mouse move
			// (terminals report motion regardless of button state) would
			// keep extending a phantom selection. The editor's own
			// hitTestLocked/selDragging checks already validate press/motion
			// positions, so forwarding everything is safe.
			if upd, ok := l.editor.(core.Updatable); ok {
				editorAdjusted := m
				editorAdjusted.Row -= l.editorTop + 1
				upd.Update(editorAdjusted)
			}
		case core.KeyMsg:
			for _, k := range terminal.ParseKeys(m.Data) {
				name := strings.ToLower(k.Name)
				switch name {
				case "v":
					if isPrimaryShortcutMod(k.Mods) &&
						k.Mods&terminal.ModAlt != 0 {
						// Ctrl+Alt+V / Cmd+Alt+V → image paste
						if l.app.cfg.OnImagePaste != nil {
							l.app.cfg.OnImagePaste()
						}
						return nil
					}
				case "escape":
					if l.ac != nil && l.ac.Active() {
						l.ac.Hide()
						// File browser: ESC navigates up one level.
						value := l.app.editor.GetValue()
						if (strings.HasPrefix(value, "@file:") || strings.HasPrefix(value, "@folder:")) &&
							len(value) > len("@file:") {
							newValue := popLastPathSegment(value)
							l.app.editor.SetValue(newValue)
							l.app.skipRefresh = false
							l.ac.Refresh(newValue, int64(len(newValue)))
						}
						return nil
					}
				case "pageUp":
					l.history.ScrollBy(-5)
				case "pageDown":
					l.history.ScrollBy(5)
				case "c", "insert":
					if isCopyShortcut(k) {
						// Prefer copying an active selection over interrupting.
						// This matters because many terminals don't report a
						// distinguishable Cmd modifier (no Kitty keyboard protocol
						// support) and send Cmd+C as the same byte sequence as
						// Ctrl+C. Without this check, Cmd+C while the agent is
						// running would interrupt it instead of copying the text
						// the user just selected.
						if hasSelection(l) {
							doCopy(l)
							return nil
						}
						// Ctrl+C while agent is running: interrupt
						if k.Mods&terminal.ModCtrl != 0 && l.app.cfg.OnInterrupt != nil && l.app.isRunning() {
							l.app.cfg.OnInterrupt()
							return nil
						}
						doCopy(l)
						return nil
					}
				}
			}
		}
	}
	if l.statusBar != nil {
		l.statusBar.Update(msg)
	}
	if l.ac != nil && l.ac.Active() {
		switch msg.(type) {
		case core.KeyMsg:
			l.ac.Update(msg)
		}
	}
	return nil
}

func (l *chatLayout) recalcMaxRows(width, height int64) {
	var headerH, loaderH, editorH, footerH, statusH, acH int64
	if l.header != nil {
		headerH = int64(len(l.header.Render(width)))
	}
	if l.editor != nil {
		editorH = int64(len(l.editor.Render(width))) + 2
	}
	if l.loader != nil && l.loader.IsRunning() {
		loaderH = int64(len(l.loader.Render(width)))
	}
	if l.footer != nil {
		footerH = int64(len(l.footer.Render(width)))
	}
	if l.statusBar != nil {
		statusH = int64(len(l.statusBar.Render(width)))
	}
	if l.ac != nil && l.ac.Active() {
		acH = int64(len(l.ac.Render(width)))
	}
	reserved := headerH + editorH + loaderH + footerH + statusH + acH
	remaining := height - reserved
	if remaining < 1 {
		remaining = 1
	}
	if l.history != nil {
		l.history.SetMaxRows(remaining)
	}
}

// popLastPathSegment removes the trailing directory or file name from a value
// like "@file:cmd/covo-agent/" → "@file:cmd/" or "@file:main.go" → "@file:".
func popLastPathSegment(value string) string {
	// Strip trailing slash if present, then remove the last segment.
	trimmed := strings.TrimSuffix(value, "/")
	idx := strings.LastIndexAny(trimmed, "/:")
	if idx < 0 {
		return value
	}
	return trimmed[:idx+1]
}
