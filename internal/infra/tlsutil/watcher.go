package tlsutil

import (
	"context"
	"os"
	"sync"
	"time"
)

// WatchFiles polls the given files at the specified interval and calls onChange
// when any file's modification time changes. Returns a cancel function to stop.
// If wg is non-nil, the watcher goroutine is tracked and the caller can wait
// for it to exit after calling cancel.
func WatchFiles(ctx context.Context, interval time.Duration, onChange func(), files ...string) (cancel context.CancelFunc) {
	return WatchFilesWithWG(ctx, interval, onChange, nil, files...)
}

// WatchFilesWithWG is like WatchFiles but accepts an optional WaitGroup for
// tracking the background goroutine.
func WatchFilesWithWG(ctx context.Context, interval time.Duration, onChange func(), wg *sync.WaitGroup, files ...string) context.CancelFunc {
	childCtx, cancel := context.WithCancel(ctx)

	modTimes := make(map[string]time.Time, len(files))
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			modTimes[f] = info.ModTime()
		}
	}

	if wg != nil {
		wg.Add(1)
	}
	go func() {
		if wg != nil {
			defer wg.Done()
		}
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
