package terminal

import (
	"os"
	"testing"
)

func TestTerminalSupportsKittyKeyboard_Detection(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T)
		want  bool
	}{
		{
			name: "kitty via KITTY_WINDOW_ID",
			setup: func(t *testing.T) {
				t.Setenv("KITTY_WINDOW_ID", "42")
				t.Setenv("TERM", "")
				t.Setenv("TERM_PROGRAM", "")
			},
			want: true,
		},
		{
			name: "ghostty TERM",
			setup: func(t *testing.T) {
				t.Setenv("KITTY_WINDOW_ID", "")
				t.Setenv("TERM", "xterm-ghostty")
				t.Setenv("TERM_PROGRAM", "")
				t.Setenv("GHOSTTY_RESOURCES_DIR", "")
				t.Setenv("FOOT_VERSION", "")
			},
			want: true,
		},
		{
			name: "apple terminal",
			setup: func(t *testing.T) {
				t.Setenv("KITTY_WINDOW_ID", "")
				t.Setenv("TERM", "xterm-256color")
				t.Setenv("TERM_PROGRAM", "Apple_Terminal")
				t.Setenv("GHOSTTY_RESOURCES_DIR", "")
				t.Setenv("FOOT_VERSION", "")
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			if got := TerminalSupportsKittyKeyboard(); got != tc.want {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestProcessTerminalKittyKbdMode(t *testing.T) {
	tm := NewProcessTerminal()
	tm.SetKittyKeyboardMode("on")
	if tm.enableKittyKeyboard != kittyKbdForceOn {
		t.Fatalf("force on not set")
	}
	tm.SetKittyKeyboardMode("off")
	if tm.enableKittyKeyboard != kittyKbdForceOff {
		t.Fatalf("force off not set")
	}
	tm.SetKittyKeyboardMode("auto")
	if tm.enableKittyKeyboard != kittyKbdAuto {
		t.Fatalf("auto not set")
	}
	tm.SetKittyKeyboardFlags(5)
	if tm.kittyFlags != 5 {
		t.Fatalf("flags=%d", tm.kittyFlags)
	}
	// flag <1 clamps to 1
	tm.SetKittyKeyboardFlags(0)
	if tm.kittyFlags != 1 {
		t.Fatalf("flags=%d want 1", tm.kittyFlags)
	}
	_ = os.Stdout // keep pkg used
}
