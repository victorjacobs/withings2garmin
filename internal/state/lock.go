package state

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

type Lock struct {
	file *flock.Flock
}

func (store *Store) AcquireLock(ctx context.Context) (*Lock, error) {
	if err := store.EnsureDirectory(); err != nil {
		return nil, err
	}

	path := filepath.Join(store.directory, "withings2garmin.lock")
	if err := verifyRegularStateFile(path); err != nil {
		return nil, err
	}

	file := flock.New(path)
	for {
		locked, err := file.TryLock()
		if err != nil {
			return nil, fmt.Errorf("acquire state lock: %w", err)
		}
		if locked {
			if err := verifyRegularStateFile(path); err != nil {
				unlockErr := file.Unlock()
				if unlockErr != nil {
					return nil, fmt.Errorf("verify state lock file: %w (also release lock: %v)", err, unlockErr)
				}
				return nil, err
			}
			return &Lock{file: file}, nil
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for state lock: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (lock *Lock) Release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	if err := lock.file.Unlock(); err != nil {
		return fmt.Errorf("release state lock: %w", err)
	}

	return nil
}
