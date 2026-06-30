// Demo2 wires every phase-2 component together:
//   - Header: TruncatedText with status.
//   - Chat history: Container holding Text and Markdown blocks.
//   - Editor: multi-line input with Markdown submission.
//   - Autocomplete overlay: /commands + @file paths.
//   - SelectList overlay: Esc-triggered palette.
//   - SettingsList overlay: Ctrl+, toggles.
//   - CancellableLoader: fake "thinking" indicator during submission.
package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	core "github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"
	"github.com/covoyage/covonaut/tui/component"
	"github.com/covoyage/covonaut/tui"
)

// ---------------------------------------------------------------------------
// Chat history component
// ---------------------------------------------------------------------------

type chatHistory struct {
	mu       sync.RWMutex
	entries  []core.Component
	app      *tui.TUI
}

func newChatHistory(app *tui.TUI) *chatHistory {
	return &chatHistory{app: app}
}

func (h *chatHistory) appendText(s string, dim bool) {
	t := component.NewText(s)
	if dim {
		t.SetBgFn(func(x string) string { return theme.CurrentPalette().Dim.Render(x) })
	}
	h.mu.Lock()
	h.entries = append(h.entries, t)
	h.mu.Unlock()
	h.app.RequestRender()
}

func (h *chatHistory) appendMarkdown(md string) {
	h.mu.Lock()
	h.entries = append(h.entries, component.NewMarkdown(md))
	h.mu.Unlock()
	h.app.RequestRender()
}

func (h *chatHistory) Render(width int64) []string {
	h.mu.RLock()
	entries := make([]core.Component, len(h.entries))
	copy(entries, h.entries)
	h.mu.RUnlock()
	var out []string
	for _, e := range entries {
		out = append(out, e.Render(width)...)
		out = append(out, core.PadToWidth("", width)) // spacer
	}
	return out
}

func (h *chatHistory) HandleInput(string) {}
func (h *chatHistory) Invalidate() {
	h.mu.RLock()
	for _, e := range h.entries {
		e.Invalidate()
	}
	h.mu.RUnlock()
}

// ---------------------------------------------------------------------------
// Header
// ---------------------------------------------------------------------------

type header struct {
	mu    sync.RWMutex
	app   *tui.TUI
	state string
}

func newHeader(app *tui.TUI) *header {
	return &header{app: app, state: "ready"}
}

func (h *header) setState(s string) {
	h.mu.Lock()
	h.state = s
	h.mu.Unlock()
	h.app.RequestRender()
}

func (h *header) Render(width int64) []string {
	h.mu.RLock()
	state := h.state
	h.mu.RUnlock()
	t := component.NewTruncatedText(fmt.Sprintf("tui-demo2 · state=%s · Ctrl+,=settings  Ctrl+K=palette  Ctrl+C=quit", state))
	t.SetPadding(1, 0)
	return t.Render(width)
}
func (h *header) HandleInput(string) {}
func (h *header) Invalidate()        {}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	term := terminal.NewProcessTerminal()
	app := tui.NewTUI(term)

	hdr := newHeader(app)
	history := newChatHistory(app)
	history.appendMarkdown(`# tui-demo2

Welcome! Try these:

- Type ` + "`/help`" + ` for slash commands.
- Type ` + "`@`" + ` followed by text for file-path completions.
- Press **Ctrl+,** to open settings.
- Press **Ctrl+K** to open the command palette.
- Press **Esc** to close overlays.

> This whole view is composed of phase-2 TUI components.`)

	editor := component.NewEditor(nil)
	editor.SetPrompt("> ", "  ")
	editor.SetPlaceholder("Type a message and press Enter…")
	editor.SetMinRows(2)
	editor.SetMaxRows(6)

	ac := component.NewAutocomplete(
		&component.StaticProvider{
			TriggerStr: "/",
			Suggestions: []core.Suggestion{
				{Label: "help", InsertText: "help", Description: "show help"},
				{Label: "clear", InsertText: "clear", Description: "clear history"},
				{Label: "theme", InsertText: "theme", Description: "cycle theme"},
				{Label: "quit", InsertText: "quit", Description: "exit"},
			},
		},
		&component.FilePathProvider{},
	)
	acOverlay := &tui.Overlay{
		Content:     ac,
		Anchor:      tui.AnchorBottomLeft,
		UseAbsolute: false,
		PercentY:    90,
		PercentX:    0,
		Width:       tui.OverlaySize{Value: 60, Percent: true, Min: 30, Max: 80},
		Height:      tui.OverlaySize{Value: 8},
		Focus:       false,
	}

	editor.OnChange(func(v string) {
		runes := []rune(v)
		ac.Refresh(v, int64(len(runes)))
		if ac.Active() {
			// Ensure overlay is on the stack exactly once.
			present := false
			for _, o := range app.Overlays() {
				if o == acOverlay {
					present = true
					break
				}
			}
			if !present {
				app.PushOverlay(acOverlay)
			}
		} else {
			app.RemoveOverlay(acOverlay)
		}
	})
	ac.OnApply(func(newValue string, _ int64, _ core.Suggestion) {
		editor.SetValue(newValue)
		app.RemoveOverlay(acOverlay)
	})

	// Settings overlay
	settings := component.NewSettingsList([]component.SettingEntry{
		{
			Key:   "theme",
			Label: "Theme",
			Options: []component.SettingOption{
				{Value: "dark", Label: "Dark"},
				{Value: "light", Label: "Light"},
				{Value: "auto", Label: "Auto"},
			},
		},
		{
			Key:   "spinner",
			Label: "Spinner",
			Options: []component.SettingOption{
				{Value: "dots", Label: "Dots"},
				{Value: "line", Label: "Line"},
				{Value: "bounce", Label: "Bounce"},
			},
		},
		{
			Key:   "markdown",
			Label: "Render Markdown",
			Options: []component.SettingOption{
				{Value: "on", Label: "On"},
				{Value: "off", Label: "Off"},
			},
		},
	})
	settingsBox := component.NewBox()
	settingsBox.SetBorder(component.BorderRounded)
	settingsBox.SetTitle("Settings")
	settingsBox.SetPadding(1, 1)
	settingsBox.AddChild(settings)
	settingsOverlay := tui.NewCenteredOverlay(settingsBox, 50, 40)
	settingsOverlay.Focus = true

	// Command palette overlay
	palette := component.NewSelectList([]component.SelectItem{
		{Value: "echo-hello", Label: "echo hello", Description: "prints hello"},
		{Value: "insert-table", Label: "insert table", Description: "inserts a markdown table"},
		{Value: "show-loader", Label: "show loader (3s)", Description: "animate CancellableLoader"},
		{Value: "toggle-settings", Label: "toggle settings", Description: "open settings overlay"},
	})
	palette.SetMaxVisible(5)
	paletteBox := component.NewBox()
	paletteBox.SetBorder(component.BorderRounded)
	paletteBox.SetTitle("Palette")
	paletteBox.SetPadding(1, 1)
	paletteBox.AddChild(palette)
	paletteOverlay := tui.NewCenteredOverlay(paletteBox, 60, 30)
	paletteOverlay.Focus = true

	closeOverlay := func(o *tui.Overlay) {
		app.RemoveOverlay(o)
		app.Focus(editor)
	}
	palette.OnCancel(func() { closeOverlay(paletteOverlay) })
	settings.OnSubmit(func(_ component.SettingEntry) { closeOverlay(settingsOverlay) })

	// CancellableLoader
	loader := component.NewCancellableLoader(app.RequestRender, "thinking…")

	palette.OnSelect(func(item component.SelectItem) {
		closeOverlay(paletteOverlay)
		switch item.Value {
		case "echo-hello":
			history.appendText("hello", false)
		case "insert-table":
			history.appendMarkdown("| col1 | col2 |\n| --- | --- |\n| aa | bb |\n| cc | dd |")
		case "toggle-settings":
			app.PushOverlay(settingsOverlay)
		case "show-loader":
			hdr.setState("busy")
			loader.Start()
			go func() {
				select {
				case <-time.After(3 * time.Second):
				case <-loader.Context().Done():
				}
				loader.Stop()
				hdr.setState("ready")
				if loader.Aborted() {
					history.appendText("(loader aborted)", true)
				} else {
					history.appendText("(loader finished)", true)
				}
			}()
		}
	})

	editor.OnSubmit(func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if v == "/quit" {
			app.Quit()
			return
		}
		if v == "/clear" {
			history.mu.Lock()
			history.entries = nil
			history.mu.Unlock()
			editor.Clear()
			app.RequestRender()
			return
		}
		history.appendText("> "+v, false)
		history.appendMarkdown("**echo**: " + v)
		editor.Clear()
	})

	// Wire global shortcuts via a thin root wrapper.
	root := &rootRouter{
		app:      app,
		editor:   editor,
		palette:  paletteOverlay,
		settings: settingsOverlay,
		loader:   loader,
	}

	app.AddChild(hdr)
	app.AddChild(history)
	app.AddChild(loader) // inline spinner under the history
	app.AddChild(editor)
	app.AddChild(root) // invisible but handles global keys when focused
	app.Focus(editor)

	if err := app.Start(); err != nil {
		fmt.Println("start error:", err)
		return
	}
	defer app.Stop()
	<-app.Done()
	fmt.Println("(tui-demo2 exited cleanly)")
}

// ---------------------------------------------------------------------------
// rootRouter — invisible component that lifts global shortcuts
// ---------------------------------------------------------------------------

type rootRouter struct {
	app      *tui.TUI
	editor   *component.Editor
	palette  *tui.Overlay
	settings *tui.Overlay
	loader   *component.CancellableLoader
}

func (r *rootRouter) Render(int64) []string { return nil }
func (r *rootRouter) Invalidate()           {}

func (r *rootRouter) HandleInput(data string) {
	switch {
	case terminal.MatchesKey(data, "ctrl+c"):
		r.app.Quit()
	case terminal.MatchesKey(data, "ctrl+k"):
		r.app.PushOverlay(r.palette)
	case terminal.MatchesKey(data, "ctrl+,"):
		r.app.PushOverlay(r.settings)
	default:
		// Forward to editor when nothing else matched.
		r.app.SendMsg(core.KeyMsg{Data: data})
	}
}

func (r *rootRouter) SetFocused(bool) {}
func (r *rootRouter) IsFocused() bool { return false }
