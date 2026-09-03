package tui

import (
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/terminal"
)

// TerminalProbe reports the terminal capabilities resolved by the engine
// (probed DA/XTVERSION replies merged with env heuristics). It returns false
// when the app's host terminal doesn't support probing or no probe ran yet.
func TerminalProbe(app *chat.ChatApp) (terminal.Capabilities, bool) {
	if app == nil {
		return terminal.Capabilities{}, false
	}
	if ph, ok := app.Host().(interface{ TerminalProbe() (terminal.Capabilities, bool) }); ok {
		return ph.TerminalProbe()
	}
	return terminal.Capabilities{}, false
}