package tui

import (
	"sync"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
)

func NewChatApp(cfg chat.ChatAppConfig) *chat.ChatApp {
	term := terminal.NewProcessTerminal()
	term.SetTerminalProbe(cfg.ProbeTerminal)
	if cfg.KittyKeyboardMode != "" {
		term.SetKittyKeyboardMode(cfg.KittyKeyboardMode)
	}
	if cfg.KittyKeyboardFlags > 0 {
		term.SetKittyKeyboardFlags(cfg.KittyKeyboardFlags)
	}
	km := terminal.NewKeybindingsManager(terminal.DefaultKeybindings())
	opts := TUIOptions{
		Keybindings:           km,
		DisableBracketedPaste: cfg.DisableBracketedPaste,
		Filter:                composeChatFilter(cfg),
	}
	// Scrollback mode writes completed messages to the terminal's native
	// scrollback (main screen), so it is mutually exclusive with the
	// alternate-screen buffer — enable one or the other, never both.
	if cfg.Scrollback {
		opts.Scrollback = true
		opts.AltScreen = false
	} else if cfg.AltScreen {
		opts.AltScreen = true
	}
	if cfg.MouseMode != "" {
		opts.MouseMode = cfg.MouseMode
	}
	app := NewTUI(term, opts)
	host := &tuiAppHost{TUI: app, scrollback: cfg.Scrollback}
	cfg.Host = host
	// Wire the TUI's lifecycle context so OnSubmit submissions are cancelled
	// when the TUI stops (instead of receiving an un-cancellable
	// context.Background()).
	if cfg.Context == nil {
		cfg.Context = app.Context()
	}
	chatApp := chat.NewChatAppWithHost(cfg, host)
	chatApp.SetHost(host)
	return chatApp
}

type tuiAppHost struct {
	*TUI
	mu       sync.Mutex
	overlays map[chat.OverlayRef]*Overlay
	// scrollback reports whether native scrollback mode is active.
	scrollback bool
}

func (h *tuiAppHost) PushOverlay(ov chat.OverlayRef) {
	wantsFocus := ov.OverlayWantsFocus()
	dimBg := ov.OverlayDimBackground()
	anchor := OverlayAnchor(ov.OverlayAnchor())
	px := ov.OverlayPercentX()
	py := ov.OverlayPercentY()
	wp := ov.OverlayWidthPct()
	hp := ov.OverlayHeightPct()

	if px == 0 {
		px = 50
	}
	if py == 0 {
		py = 50
	}
	if wp == 0 {
		wp = 70
	}
	if hp == 0 {
		hp = 70
	}

	o := &Overlay{
		Content:       ov.OverlayContent(),
		Focus:         wantsFocus,
		DimBackground: dimBg,
		Anchor:        anchor,
		PercentX:      int64(px),
		PercentY:      int64(py),
		Width:         OverlaySize{Value: int64(wp), Percent: true, Min: 10},
		Height:        OverlaySize{Value: int64(hp), Percent: true, Min: 3},
	}
	ov.SetOverlayFocus(o.Focus)
	ov.SetOverlayDimBackground(o.DimBackground)
	h.mu.Lock()
	if h.overlays == nil {
		h.overlays = make(map[chat.OverlayRef]*Overlay)
	}
	h.overlays[ov] = o
	// PushOverlay must happen under h.mu so the overlays map and the TUI
	// overlay stack update atomically. Otherwise a concurrent RemoveOverlay
	// could find the map entry missing while the overlay was already pushed
	// to the TUI stack (leak), or vice-versa. Lock order h.mu -> TUI.mu is
	// consistent with RemoveOverlay below.
	h.TUI.PushOverlay(o)
	h.mu.Unlock()
}

func (h *tuiAppHost) RemoveOverlay(ov chat.OverlayRef) bool {
	h.mu.Lock()
	top := h.overlays[ov]
	if top != nil {
		delete(h.overlays, ov)
		// Mirror PushOverlay: do the TUI stack mutation under h.mu so the
		// map delete and stack removal stay in sync.
		removed := h.TUI.RemoveOverlay(top)
		h.mu.Unlock()
		return removed
	}
	h.mu.Unlock()
	return false
}

func (h *tuiAppHost) TerminalSize() (cols, rows int64) {
	return h.TUI.Terminal().Size()
}

// TerminalProbe returns the terminal's resolved capabilities, when the
// underlying terminal supports reporting them.
func (h *tuiAppHost) TerminalProbe() (terminal.Capabilities, bool) {
	tm := h.TUI.Terminal()
	if cp, ok := tm.(terminal.CapabilitiesProvider); ok {
		return cp.Capabilities(), true
	}
	return terminal.Capabilities{}, false
}

// WriteScrollback writes rendered lines directly to the terminal's native
// scrollback buffer. In scrollback mode the TUI renders into a bottom-pinned
// scroll region, so lines written here persist above the live area instead
// of being overwritten by the next repaint.
func (h *tuiAppHost) WriteScrollback(lines []string) {
	if !h.scrollback || len(lines) == 0 {
		return
	}
	h.WriteNativeScrollback(lines)
}

func composeChatFilter(cfg chat.ChatAppConfig) func(core.Component, core.Msg) core.Msg {
	userFilter := cfg.Filter
	onImage := cfg.OnImagePaste
	if userFilter == nil && onImage == nil {
		return nil
	}
	return func(c core.Component, msg core.Msg) core.Msg {
		if userFilter != nil {
			msg = userFilter(c, msg)
			if msg == nil {
				return nil
			}
		}
		paste, ok := msg.(core.PasteMsg)
		if !ok {
			return msg
		}
		if paste.Text == "" || (len(paste.Text) < 4 && paste.Text == "\r") {
			if onImage != nil {
				onImage()
				return nil
			}
		}
		return msg
	}
}
