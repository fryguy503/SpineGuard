package reader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// OS locks are released even if a detached reader terminates unexpectedly. A
// separate persistent lock file keeps the lock stable across atomic renames.
func lockState(ctx context.Context, dir string) (func(), error) {
	if dir == "" {
		return func() {}, nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "reader-state.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		locked, err := tryStateFileLock(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		if locked {
			return func() { unlockStateFile(file); file.Close() }, nil
		}
		timer := time.NewTimer(15 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			file.Close()
			return nil, fmt.Errorf("saved reading state is busy: %w", ctx.Err())
		case <-timer.C:
		}
	}
}
