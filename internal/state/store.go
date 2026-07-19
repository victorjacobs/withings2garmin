package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
