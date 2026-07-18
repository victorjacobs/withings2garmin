package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

var (
	ErrNotFound = errors.New("state file not found")
	ErrCorrupt  = errors.New("state file is corrupt")
	ErrWrite    = errors.New("state file write failed")
)

type Store struct {
	directory string
}

func NewStore(directory string) *Store {
	return &Store{directory: directory}
}

func (store *Store) Directory() string {
	return store.directory
}

func (store *Store) EnsureDirectory() error {
	if store.directory == "" {
		return fmt.Errorf("state directory is empty")
	}

	info, err := os.Lstat(store.directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(store.directory, 0o700); err != nil {
			return fmt.Errorf("create state directory %q: %w", store.directory, err)
		}
		info, err = os.Lstat(store.directory)
	}
	if err != nil {
		return fmt.Errorf("inspect state directory %q: %w", store.directory, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state directory %q must not be a symlink", store.directory)
	}
	if !info.IsDir() {
		return fmt.Errorf("state path %q is not a directory", store.directory)
	}
	if err := verifyOwnership(info, store.directory); err != nil {
		return err
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(store.directory, 0o700); err != nil {
			return fmt.Errorf("tighten state directory permissions %q: %w", store.directory, err)
		}
	}

	return nil
}

func (store *Store) LoadWithingsTokens() (WithingsTokens, error) {
	var tokens WithingsTokens
	err := store.load("withings-tokens.json", &tokens, func() error { return tokens.validate() })
	return tokens, err
}

func (store *Store) SaveWithingsTokens(tokens WithingsTokens) error {
	if tokens.SchemaVersion == 0 {
		tokens.SchemaVersion = SchemaVersion
	}
	if err := tokens.validate(); err != nil {
		return fmt.Errorf("validate Withings tokens: %w", err)
	}
	return store.save("withings-tokens.json", tokens)
}

func (store *Store) LoadGarminTokens() (GarminTokens, error) {
	var tokens GarminTokens
	err := store.load("garmin-tokens.json", &tokens, func() error { return tokens.validate() })
	return tokens, err
}

func (store *Store) SaveGarminTokens(tokens GarminTokens) error {
	if tokens.SchemaVersion == 0 {
		tokens.SchemaVersion = SchemaVersion
	}
	if err := tokens.validate(); err != nil {
		return fmt.Errorf("validate Garmin tokens: %w", err)
	}
	return store.save("garmin-tokens.json", tokens)
}

func (store *Store) LoadSyncState() (SyncState, error) {
	var syncState SyncState
	err := store.load("sync-state.json", &syncState, func() error { return syncState.validate() })
	return syncState, err
}

func (store *Store) SaveSyncState(syncState SyncState) error {
	if syncState.SchemaVersion == 0 {
		syncState.SchemaVersion = SchemaVersion
	}
	if syncState.Ledger == nil {
		syncState.Ledger = make(map[int64]LedgerEntry)
	}
	if err := syncState.validate(); err != nil {
		return fmt.Errorf("validate sync state: %w", err)
	}
	return store.save("sync-state.json", syncState)
}

func (store *Store) load(name string, target any, validate func() error) error {
	if err := store.EnsureDirectory(); err != nil {
		return err
	}

	path := filepath.Join(store.directory, name)
	if err := verifyRegularStateFile(path); err != nil {
		return err
	}

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	if err != nil {
		return fmt.Errorf("open state file %q: %w", path, err)
	}

	decoder := json.NewDecoder(io.LimitReader(file, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return fmt.Errorf("decode state file %q: %w (also close: %v)", path, err, closeErr)
		}
		return fmt.Errorf("%w: decode state file %q: %v", ErrCorrupt, path, err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return fmt.Errorf("parse state file %q: %w (also close: %v)", path, err, closeErr)
		}
		return fmt.Errorf("%w: parse state file %q: %v", ErrCorrupt, path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close state file %q: %w", path, err)
	}
	if err := validate(); err != nil {
		return fmt.Errorf("%w: invalid state file %q: %v", ErrCorrupt, path, err)
	}

	return nil
}

func (store *Store) save(name string, value any) error {
	if err := store.EnsureDirectory(); err != nil {
		return err
	}

	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal state file %q: %w", name, err)
	}
	data = append(data, '\n')

	path := filepath.Join(store.directory, name)
	if err := verifyRegularStateFile(path); err != nil {
		return err
	}
	if err := atomicReplace(path, data); err != nil {
		return fmt.Errorf("%w: write state file %q: %v", ErrWrite, path, err)
	}

	return nil
}

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

func rejectTrailingJSON(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func validateSchemaVersion(version int) error {
	if version != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d (supported: %d)", version, SchemaVersion)
	}

	return nil
}
