package config

import (
	"path/filepath"
	"testing"
)

func TestResolveStateDir(t *testing.T) {
	directory, err := ResolveStateDir("/var/lib/garmin-import/../garmin-import")
	if err != nil {
		t.Fatalf("ResolveStateDir() error = %v", err)
	}
	if directory != "/var/lib/garmin-import" {
		t.Fatalf("ResolveStateDir() = %q", directory)
	}

	if _, err := ResolveStateDir(filepath.Join("relative", "state")); err == nil {
		t.Fatal("ResolveStateDir() accepted a relative path")
	}
}

func TestParseLogLevel(t *testing.T) {
	level, err := ParseLogLevel("WARN")
	if err != nil || level != LogLevelWarn {
		t.Fatalf("ParseLogLevel() = %q, %v", level, err)
	}
	if _, err := ParseLogLevel("loud"); err == nil {
		t.Fatal("ParseLogLevel() accepted an unknown level")
	}
}
