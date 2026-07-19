package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func atomicReplace(path string, data []byte) (resultErr error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if resultErr != nil {
			if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				resultErr = fmt.Errorf("%w (also remove temporary file: %v)", resultErr, err)
			}
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return closeAfterFailure(temporary, "set temporary file permissions", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return closeAfterFailure(temporary, "write temporary file", err)
	}
	if err := temporary.Sync(); err != nil {
		return closeAfterFailure(temporary, "sync temporary file", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("sync state directory: %w", err)
	}

	return nil
}

func closeAfterFailure(file *os.File, operation string, operationErr error) error {
	if closeErr := file.Close(); closeErr != nil {
		return fmt.Errorf("%s: %w (also close temporary file: %v)", operation, operationErr, closeErr)
	}

	return fmt.Errorf("%s: %w", operation, operationErr)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		closeErr := directory.Close()
		if closeErr != nil {
			return fmt.Errorf("%w (also close: %v)", err, closeErr)
		}

		return err
	}

	return directory.Close()
}
