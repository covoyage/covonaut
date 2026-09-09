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

const thinkingTruncateLines = 8

// DefaultReasoningRenderer preserves the reasoning display policy:
// thinking segments are shown (or hidden) according to Show and Mode.
//
//   - Show=false or Mode="hide": emit nothing (segments stay in the message).
//   - Mode="summary" / "collapsed": one-line summary per segment.
//   - Mode="truncated": first thinkingTruncateLines of each segment, then "…".
//   - Mode="full": always expand every segment.
//   - Per-segment Collapsed still wins for summary/truncated unless Mode=full.
type DefaultReasoningRenderer struct {
	Show   bool
	Stream bool   // when false, hide thinking on pending (streaming) messages
	Mode   string // "hide" / "summary"|"collapsed" / "truncated" / "full"
}

// RenderThinking uses a pointer receiver so that runtime mutations to
// Show/Mode on a *DefaultReasoningRenderer actually take effect. A value
// receiver would snapshot the fields at the moment the value was assigned
// to the ReasoningRenderer interface, making later tweaks invisible.
func (r *DefaultReasoningRenderer) RenderThinking(m ChatMessage, width int64) []string {
	if r == nil || !r.Show {
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(r.Mode))
	if mode == "hide" {
		return nil
	}
	if m.Pending && !r.Stream {
		return nil
	}
	pal := theme.CurrentPalette()
	var out []string
	for idx, seg := range m.ThinkingSegments {
		if seg.Text == "" {
			continue
		}
		rawLines := strings.Split(seg.Text, "\n")
		lineCount := len(rawLines)

		collapsed := seg.Collapsed
		if mode == "full" {
			collapsed = false
		}
		if mode == "summary" || mode == "collapsed" {
			collapsed = true
		}

		switch {
		case collapsed:
			summary := fmt.Sprintf("💭 Thinking (%d lines)", lineCount)
			out = append(out, pal.Thinking.Render(summary))
		case mode == "truncated" && lineCount > thinkingTruncateLines:
			out = append(out, pal.Thinking.Render("💭 Thinking"))
			shown := strings.Join(rawLines[:thinkingTruncateLines], "\n")
			for _, line := range core.WrapAnsi(pal.Thinking.Render(shown), width) {
				out = append(out, "  "+line)
			}
			out = append(out, pal.Dim.Render(fmt.Sprintf("  … %d lines", lineCount-thinkingTruncateLines)))
		default:
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
