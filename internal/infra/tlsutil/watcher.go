package tlsutil

import (
	"context"
	"os"
	"time"
)

// WatchFiles polls the given files at the specified interval and calls onChange
// when any file's modification time changes. Returns a cancel function to stop.
func WatchFiles(ctx context.Context, interval time.Duration, onChange func(), files ...string) context.CancelFunc {
	childCtx, cancel := context.WithCancel(ctx)

	modTimes := make(map[string]time.Time, len(files))
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			modTimes[f] = info.ModTime()
		}
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-childCtx.Done():
				return
			case <-ticker.C:
				for _, f := range files {
					info, err := os.Stat(f)
					if err != nil {
						continue
					}
					if prev, ok := modTimes[f]; !ok || !info.ModTime().Equal(prev) {
						modTimes[f] = info.ModTime()
						onChange()
						break
					}
				}
			}
		}
	}()

	return cancel
}
