//go:build !windows

package tools

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// signalByName maps a signal name to its syscall value.
func signalByName(name string) (syscall.Signal, bool) {
	sig, ok := map[string]syscall.Signal{
		"SIGINT":  syscall.SIGINT,
		"SIGTERM": syscall.SIGTERM,
		"SIGHUP":  syscall.SIGHUP,
		"SIGQUIT": syscall.SIGQUIT,
		"SIGKILL": syscall.SIGKILL,
	}[name]
	return sig, ok
}

// sendSignal delivers sig to the terminal's foreground process group, which
// reproduces Ctrl-C semantics: the interactive shell typically runs jobs in
// their own process group, so signalling the shell's group alone would miss
// the foreground command. Falls back to the shell's group, then to the shell
// process itself.
func (s *terminalSession) sendSignal(sig syscall.Signal) error {
	if s.tty != nil {
		if fg, err := unix.IoctlGetInt(int(s.tty.Fd()), unix.TIOCGPGRP); err == nil && fg > 0 {
			if err := syscall.Kill(-fg, sig); err == nil {
				return nil
			}
		}
	}
	if err := syscall.Kill(-s.cmd.Process.Pid, sig); err != nil {
		return s.cmd.Process.Signal(sig)
	}
	return nil
}
