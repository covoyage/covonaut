package core

import "sync"

// ---------------------------------------------------------------------------
// Component interface
//
// A Component renders itself into a slice of lines, one per terminal row.
// Each returned line MUST NOT exceed `width` visible columns; callers
// (the TUI container) will otherwise error or clip.
//
// handleInput is called only when the component currently owns focus and
// receives raw terminal input (possibly containing ANSI escapes). Use
// ParseKeys / MatchesKey / KeybindingsManager to interpret it.
//
// Invalidate is called by the container whenever cached rendering state
// must be discarded (e.g. theme changed, width changed).
// ---------------------------------------------------------------------------

// Component is the core interface every renderable element implements.
type Component interface {
	Render(width int64) []string
	Invalidate()
}

// Updatable is the interface that components implement to participate in
// the message-driven update cycle. All user interaction (keys, mouse,
// paste, resize) and application events are delivered as Msg values
// through Update, following the Elm Architecture pattern.
//
// Update mutates component state and optionally returns a Cmd for
// asynchronous side effects.
type Updatable interface {
	Update(msg Msg) Cmd
}

// Focusable marks a component that can hold a visible hardware cursor
// (needed for IME candidate-window positioning with CJK input methods).
//
// A focusable component should emit CURSOR_MARKER at the cursor cell when
// SetFocused(true) has been called on it. The TUI container will strip the
// marker from output and position the real hardware cursor at its location.
type Focusable interface {
	SetFocused(focused bool)
	IsFocused() bool
}

// CURSOR_MARKER is a zero-width APC (Application Program Command) escape
// sequence terminals ignore but the TUI recognises. Place it in rendered
// output at the desired cursor column.
const CURSOR_MARKER = "\x1b_pi:c\x07"

// WantsKeyRelease is an optional marker interface. Components that want
// Kitty key-release events must implement it and return true; otherwise
// the container filters release events out.
type WantsKeyRelease interface {
	WantsKeyRelease() bool
}

// ---------------------------------------------------------------------------
// Container — the canonical composite Component.
// ---------------------------------------------------------------------------

// Container renders a vertical stack of child components.
type Container struct {
	mu       sync.RWMutex
	children []Component
}

// NewContainer returns an empty container.
func NewContainer() *Container { return &Container{} }

// AddChild appends a component to the end.
func (c *Container) AddChild(child Component) {
	if child == nil {
		return
	}
	c.mu.Lock()
	c.children = append(c.children, child)
	c.mu.Unlock()
}

// RemoveChild removes the first occurrence of child. Returns true if removed.
func (c *Container) RemoveChild(child Component) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, ch := range c.children {
		if ch == child {
			c.children = append(c.children[:i], c.children[i+1:]...)
			return true
		}
	}
	return false
}

// Clear removes all children.
func (c *Container) Clear() {
	c.mu.Lock()
	c.children = nil
	c.mu.Unlock()
}

// Children returns a snapshot slice of the current children.
func (c *Container) Children() []Component {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Component, len(c.children))
	copy(out, c.children)
	return out
}

// Render concatenates child renders vertically.
func (c *Container) Render(width int64) []string {
	c.mu.RLock()
	children := make([]Component, len(c.children))
	copy(children, c.children)
	c.mu.RUnlock()

	var lines []string
	for _, ch := range children {
		if ch == nil {
			continue
		}
		lines = append(lines, ch.Render(width)...)
	}
	return lines
}

// Invalidate fans out to all children.
func (c *Container) Invalidate() {
	c.mu.RLock()
	children := make([]Component, len(c.children))
	copy(children, c.children)
	c.mu.RUnlock()
	for _, ch := range children {
		ch.Invalidate()
	}
}

// Suggestion is one completion candidate.
type Suggestion struct {
	// Label is the visible label in the list.
	Label string
	// InsertText replaces the active token (required).
	InsertText string
	// Description is an optional dim suffix shown next to the label.
	Description string
	// Tag is an opaque value callers can inspect on apply.
	Tag any
}

// AutocompleteProvider supplies suggestions for a trigger.
type AutocompleteProvider interface {
	// Trigger returns the prefix characters that activate this provider
	// (e.g. "/", "@"). An empty trigger means "always consider".
	Trigger() string
	// Complete returns suggestions for the token. `token` excludes the
	// trigger prefix.
	Complete(token string) []Suggestion
}

// ---------------------------------------------------------------------------
// Small helpers for component authors.
// ---------------------------------------------------------------------------

// EnsureWidth returns lines truncated so none exceeds width cells. It does
// NOT pad to exactly width; callers may pad manually for background fills.
func EnsureWidth(lines []string, width int64) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if VisibleWidth(l) > width {
			out[i] = TruncateToWidth(l, width, "…")
		} else {
			out[i] = l
		}
	}
	return out
}

// FillLines returns `count` empty lines of the given width (all spaces).
func FillLines(count, width int64) []string {
	if count <= 0 {
		return nil
	}
	line := ""
	if width > 0 {
		line = PadToWidth("", width)
	}
	out := make([]string, count)
	for i := range out {
		out[i] = line
	}
	return out
}
