package terminal

// ---------------------------------------------------------------------------
// ANSI escape sequence builders
//
// These are pure functions that return ANSI control strings. They do not
// perform I/O themselves — callers write the result to a terminal or writer.
// Moved from the theme package; these are terminal I/O primitives, not
// styling concerns.
// ---------------------------------------------------------------------------

// ANSI escape sequence constants.
const (
	Esc   = "\033[" // CSI introducer
	Reset = Esc + "0m"
)

// Cursor movement (return ANSI strings; do not perform I/O).
func CursorUp(n int64) string    { return escn("A", n) }
func CursorDown(n int64) string  { return escn("B", n) }
func CursorRight(n int64) string { return escn("C", n) }
func CursorLeft(n int64) string  { return escn("D", n) }

// Screen clearing.
func ClearLine() string   { return Esc + "2K" }
func ClearToEnd() string  { return Esc + "0K" }
func ClearScreen() string { return Esc + "2J" + Esc + "H" }

// Cursor save/restore.
func SaveCursor() string    { return Esc + "s" }
func RestoreCursor() string { return Esc + "u" }

// Cursor visibility.
func HideCursor() string { return Esc + "?25l" }
func ShowCursor() string { return Esc + "?25h" }

// escn formats "ESC<n>Suffix" (e.g. "ESC[5A" for cursor up 5).
func escn(suffix string, n int64) string {
	if n <= 0 {
		return ""
	}
	return Esc + itoa(n) + suffix
}

// itoa converts int64 to string without strconv dependency.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
