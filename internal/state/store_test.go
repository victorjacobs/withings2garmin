package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreCreatesRestrictiveStateFiles(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state"))
	tokens := WithingsTokens{AccessToken: "access", RefreshToken: "refresh"}
	if err := store.SaveWithingsTokens(tokens); err != nil {
		t.Fatal(err)
	}

	directoryInfo, err := os.Stat(store.Directory())
	if err != nil {
		t.Fatal(err)
	}
	if permissions := directoryInfo.Mode().Perm(); permissions != 0o700 {
		t.Fatalf("directory permissions = %o", permissions)
	}
	fileInfo, err := os.Stat(filepath.Join(store.Directory(), "withings-tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if permissions := fileInfo.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("file permissions = %o", permissions)
	}

	loaded, err := store.LoadWithingsTokens()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AccessToken != tokens.AccessToken || loaded.SchemaVersion != SchemaVersion {
		t.Fatalf("LoadWithingsTokens() = %#v", loaded)
	}
}

func TestStoreDoesNotReplaceExistingFileWhenMarshalFails(t *testing.T) {
	store := NewStore(t.TempDir())
	tokens := WithingsTokens{AccessToken: "old", RefreshToken: "refresh"}
	if err := store.SaveWithingsTokens(tokens); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(store.Directory(), "withings-tokens.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.save("withings-tokens.json", make(chan int)); err == nil {
		t.Fatal("save() succeeded for an unmarshalable value")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed write changed the active state file")
	}
}

func TestStoreRejectsCorruptAndFutureSchema(t *testing.T) {
	store := NewStore(t.TempDir())
	for name, contents := range map[string]string{
		"corrupt": "{",
		"future":  `{"schema_version":2,"ledger":{}}`,
	} {
		if err := os.WriteFile(filepath.Join(store.Directory(), "sync-state.json"), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.LoadSyncState(); err == nil {
			t.Fatalf("LoadSyncState() accepted %s input", name)
		}
	}
}

func TestStoreRejectsSymlink(t *testing.T) {
	store := NewStore(t.TempDir())
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte(`{"schema_version":1,"ledger":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(store.Directory(), "sync-state.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := store.LoadSyncState(); err == nil {
		t.Fatal("LoadSyncState() accepted a symlink")
	}
}

func TestStoreMissingFileHasSentinel(t *testing.T) {
	store := NewStore(t.TempDir())
	_, err := store.LoadGarminTokens()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadGarminTokens() error = %v", err)
	}
}

func TestSyncStateValidation(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	syncState := NewSyncState()
	syncState.Ledger[42] = LedgerEntry{GroupID: 42, State: LedgerPending, FirstSeenAt: now, LastSeenAt: now}
	if err := store.SaveSyncState(syncState); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireLockHonorsContext(t *testing.T) {
	store := NewStore(t.TempDir())
	first, err := store.AcquireLock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := first.Release(); err != nil {
			t.Error(err)
		}
	}()

	context, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := store.AcquireLock(context); err == nil {
		t.Fatal("AcquireLock() acquired an already-held lock")
	}
}
