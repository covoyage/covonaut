//go:build windows

package tools

import (
	"os"
	"syscall"
)

// signalByName is unavailable on Windows; terminal_signal reports the
// limitation through the unsupported return value.
func signalByName(name string) (syscall.Signal, bool) {
	return 0, false
}

// sendSignal delivers sig to the terminal session process. Windows has no
// process groups, so the signal goes to the shell process itself.
func (s *terminalSession) sendSignal(sig syscall.Signal) error {
	return s.cmd.Process.Signal(os.Interrupt)
}
