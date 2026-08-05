//go:build !windows

package terminal

import (
	"os"
	"os/signal"
	"syscall"
)

func startResizeNotifications(events chan os.Signal) func() {
	signal.Notify(events, syscall.SIGWINCH)
	return func() { signal.Stop(events) }
}
