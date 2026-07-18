package secret

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileOnlyRemovesOneTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("  retains spaces  \r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	value, err := ReadFile(path, "test secret")
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "  retains spaces  " {
		t.Fatalf("ReadFile() = %q", value)
	}
}

func TestReadFileRejectsEmptyAndNUL(t *testing.T) {
	directory := t.TempDir()
	for name, contents := range map[string][]byte{
		"empty": nil,
		"nul":   []byte("a\x00b"),
	} {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadFile(path, "test secret"); err == nil {
			t.Fatalf("ReadFile(%q) succeeded", name)
		}
	}
}
