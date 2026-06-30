//go:build windows

package tools

import "os/exec"

func applySubprocessIsolation(cmd *exec.Cmd) {
	// Windows: no terminal isolation needed (no /dev/tty concept).
	// Stdin is already set to nil.
}
