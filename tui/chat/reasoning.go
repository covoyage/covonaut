package chat

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"
)

// ReasoningRenderer renders the thinking segments of a ChatMessage into
// terminal lines. Inject a custom implementation to control how reasoning
// blocks are displayed (collapsed, truncated, sidebar, hidden, etc.).
//
// The renderer is called once per assistant message during Render; it
// receives the full message (including ThinkingSegments) and the available
// width in cells. It returns the lines to emit, or nil/empty to emit nothing.
type ReasoningRenderer interface {
	RenderThinking(msg ChatMessage, width int64) []string
}

// HiddenReasoningRenderer emits nothing for thinking segments. Use this to
// suppress reasoning display entirely.
type HiddenReasoningRenderer struct{}

func (HiddenReasoningRenderer) RenderThinking(_ ChatMessage, _ int64) []string { return nil }

// DefaultReasoningRenderer preserves the legacy reasoning display policy:
// thinking segments are shown (or hidden) and collapsed (or expanded)
// according to the Show and Mode fields.
//
//   - Show=false: emit nothing.
//   - Mode="full": always expand every segment.
//   - Mode="truncated": respect per-segment Collapsed flag (legacy behaviour
//     kept "truncated" semantically equivalent to non-"full" for now).
//   - Mode="collapsed" (or any other value): respect per-segment Collapsed flag.
type DefaultReasoningRenderer struct {
	Show bool
	Mode string // "collapsed" / "truncated" / "full"
}

// RenderThinking uses a pointer receiver so that runtime mutations to
// Show/Mode on a *DefaultReasoningRenderer actually take effect. A value
// receiver would snapshot the fields at the moment the value was assigned
// to the ReasoningRenderer interface, making later tweaks invisible.
func (r *DefaultReasoningRenderer) RenderThinking(m ChatMessage, width int64) []string {
	if !r.Show {
		return nil
	}
	pal := theme.CurrentPalette()
	var out []string
	for idx, seg := range m.ThinkingSegments {
		if seg.Text == "" {
			continue
		}
		lineCount := len(strings.Split(seg.Text, "\n"))

		collapsed := seg.Collapsed
		if r.Mode == "full" {
			collapsed = false
		}
		// "truncated" / "collapsed" / any other value all just respect the
		// per-segment Collapsed flag (already captured in `collapsed`), so no
		// extra branch is needed.

		if collapsed {
			summary := fmt.Sprintf("💭 Thinking (%d lines)", lineCount)
			out = append(out, pal.Thinking.Render(summary))
		} else {
			out = append(out, pal.Thinking.Render("💭 Thinking"))
			for _, line := range core.WrapAnsi(pal.Thinking.Render(seg.Text), width) {
				out = append(out, "  "+line)
			}
		}

		if idx < len(m.ThinkingSegments)-1 || m.Text != "" {
			out = append(out, "")
		}
	}
	return out
}
