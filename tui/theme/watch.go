package theme

import (
	"context"
	"os"
	"time"
)

// StartSemanticThemeWatcher polls a theme JSON path and reapplies the palette
// when the file mtime changes. Returns a cancel function. Zero poll uses 800ms.
func StartSemanticThemeWatcher(path string, poll time.Duration, onReload func()) func() {
	if path == "" {
		return func() {}
	}
	if poll <= 0 {
		poll = 800 * time.Millisecond
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		var lastMtime int64 = -1
		t := time.NewTicker(poll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				st, err := os.Stat(path)
				if err != nil {
					continue
				}
				mt := st.ModTime().UnixNano()
				if lastMtime < 0 {
					_ = LoadSemanticThemeFromFile(path, ColorModeFromEnv())
					lastMtime = mt
					continue
				}
				if mt == lastMtime {
					continue
				}
				lastMtime = mt
				_ = LoadSemanticThemeFromFile(path, ColorModeFromEnv())
				if onReload != nil {
					onReload()
				}
			}
		}
	}()
	return cancel
}
