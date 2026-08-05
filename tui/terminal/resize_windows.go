//go:build windows

package terminal

import (
	"os"
	"time"
)

type resizeEvent struct{}

func (resizeEvent) Signal()        {}
func (resizeEvent) String() string { return "terminal resize poll" }

func startResizeNotifications(events chan os.Signal) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				select {
				case events <- resizeEvent{}:
				default:
				}
			case <-stop:
				return
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}
