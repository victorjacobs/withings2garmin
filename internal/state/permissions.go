package state

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
)

func verifyRegularStateFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect state file %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state file %q must not be a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("state path %q is not a regular file", path)
	}
	if err := verifyOwnership(info, path); err != nil {
		return err
	}
	if info.Mode().Perm() != 0o600 {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("tighten state file permissions %q: %w", path, err)
		}
	}

	return nil
}

func verifyOwnership(info os.FileInfo, path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("inspect ownership for %q", path)
	}
	if int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("state path %q is owned by UID %d, not current UID %d", path, stat.Uid, os.Geteuid())
	}

	return nil
}
