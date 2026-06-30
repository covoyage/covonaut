package chat

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// CopyToClipboard writes text to the system clipboard.
// It tries native tools first (pbcopy/xclip/clip), then falls back to OSC 52
// for terminals that support it (iTerm2, Terminal.app, Kitty, WezTerm, Ghostty, VS Code, etc).
func CopyToClipboard(text string) error {
	// Try native platform tools first.
	err := copyNative(text)
	if err == nil {
		return nil
	}

	// Fallback to OSC 52 terminal escape sequence.
	return copyOSC52(text)
}

func copyNative(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		if p, _ := exec.LookPath("xclip"); p != "" {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if p, _ := exec.LookPath("xsel"); p != "" {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else {
			return fmt.Errorf("no clipboard command found")
		}
	case "windows":
		cmd = exec.Command("clip.exe")
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// copyOSC52 writes text to clipboard using the OSC 52 terminal escape sequence.
// Supported terminals: iTerm2, Terminal.app (macOS 13.4+), Kitty, WezTerm, Ghostty,
// Alacritty, VS Code, Cursor, foot, tmux (with allow-passthrough on), and many others.
func copyOSC52(text string) error {
	// OSC 52 format: ESC ] 52 ; c ; <base64> BEL (or ST)
	// 'c' = clipboard selection
	encoded := base64.StdEncoding.EncodeToString([]byte(text))

	// Truncate if too long (OSC 52 has practical limits ~100KB)
	const maxLen = 100_000
	if len(encoded) > maxLen {
		encoded = encoded[:maxLen]
	}

	// Write the OSC 52 sequence to stdout
	osc := fmt.Sprintf("\x1b]52;c;%s\x07", encoded)
	_, err := fmt.Print(osc)
	return err
}

// CopyToClipboardOSC52 forces OSC 52 copy (useful for SSH sessions or tmux).
func CopyToClipboardOSC52(text string) error {
	return copyOSC52(text)
}
