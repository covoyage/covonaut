package tui

import (
	"testing"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
)

type bridgeTestComponent struct{}

func (bridgeTestComponent) Render(width int64) []string { return []string{"overlay"} }
func (bridgeTestComponent) Invalidate()                 {}

type bridgeTestOverlayRef struct {
	content core.Component
	focus   bool
	dim     bool
}

func (o *bridgeTestOverlayRef) OverlayContent() core.Component { return o.content }
func (o *bridgeTestOverlayRef) SetOverlayFocus(v bool)         { o.focus = v }
func (o *bridgeTestOverlayRef) SetOverlayDimBackground(v bool) { o.dim = v }
func (o *bridgeTestOverlayRef) OverlayWantsFocus() bool        { return o.focus }
func (o *bridgeTestOverlayRef) OverlayDimBackground() bool     { return o.dim }
func (o *bridgeTestOverlayRef) OverlayAnchor() int             { return 0 }
func (o *bridgeTestOverlayRef) OverlayPercentX() int           { return 0 }
func (o *bridgeTestOverlayRef) OverlayPercentY() int           { return 0 }
func (o *bridgeTestOverlayRef) OverlayWidthPct() int           { return 0 }
func (o *bridgeTestOverlayRef) OverlayHeightPct() int          { return 0 }

var _ chat.OverlayRef = (*bridgeTestOverlayRef)(nil)

func TestTuiAppHostRemovesOverlayByRef(t *testing.T) {
	tui := NewTUI(terminal.NewVirtualTerminal(80, 24), TUIOptions{})
	host := &tuiAppHost{TUI: tui}
	ref := &bridgeTestOverlayRef{content: bridgeTestComponent{}, focus: true}

	host.PushOverlay(ref)
	if got := len(tui.Overlays()); got != 1 {
		t.Fatalf("expected 1 overlay after push, got %d", got)
	}

	if !host.RemoveOverlay(ref) {
		t.Fatal("expected RemoveOverlay to remove pushed overlay")
	}
	if got := len(tui.Overlays()); got != 0 {
		t.Fatalf("expected no overlays after remove, got %d", got)
	}
}
