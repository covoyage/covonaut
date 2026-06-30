//go:build !darwin && !linux

package terminal

import "errors"

// Fallback stubs for platforms where termios ioctl details are not yet
// implemented. ProcessTerminal will return an error from Start(); callers
// can still use VirtualTerminal for tests.

type termios struct{}

func getTermios(fd uintptr) (termios, error) {
	return termios{}, errors.New("tui: raw mode not implemented on this platform")
}

func setTermios(fd uintptr, t *termios) error {
	return errors.New("tui: raw mode not implemented on this platform")
}

func makeRaw(orig termios) termios { return orig }

func getWinsize(fd uintptr) (cols, rows int64, err error) {
	return 80, 24, nil
}
