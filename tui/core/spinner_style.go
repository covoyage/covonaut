package core

import "time"

// SpinnerStyle defines the animation frames and speed for a spinner indicator.
// This is a pure data type with no rendering dependency, placed in core so
// both the TUI component layer (Loader) and the procedural stdio layer
// (Spinner) can share it without creating cross-dependencies.
type SpinnerStyle struct {
	Frames   []string
	Interval time.Duration
}

var (
	SpinnerDots = SpinnerStyle{
		Frames:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		Interval: 80 * time.Millisecond,
	}
	SpinnerLine = SpinnerStyle{
		Frames:   []string{"-", "\\", "|", "/"},
		Interval: 100 * time.Millisecond,
	}
	SpinnerBounce = SpinnerStyle{
		Frames:   []string{"⠁", "⠂", "⠄", "⡀", "⢀", "⠠", "⠐", "⠈"},
		Interval: 100 * time.Millisecond,
	}
	SpinnerGlobe = SpinnerStyle{
		Frames:   []string{"🌍", "🌎", "🌏"},
		Interval: 200 * time.Millisecond,
	}
	SpinnerMoon = SpinnerStyle{
		Frames:   []string{"🌑", "🌒", "🌓", "🌔", "🌕", "🌖", "🌗", "🌘"},
		Interval: 150 * time.Millisecond,
	}
	SpinnerCircle = SpinnerStyle{
		Frames:   []string{"◐", "◓", "◑", "◒"},
		Interval: 120 * time.Millisecond,
	}
)
