package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ApplicationName = "withings2garmin"

type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

func (level LogLevel) Valid() bool {
	switch level {
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
		return true
	default:
		return false
	}
}

func ParseLogLevel(value string) (LogLevel, error) {
	level := LogLevel(strings.ToLower(value))
	if !level.Valid() {
		return "", fmt.Errorf("invalid log level %q", value)
	}

	return level, nil
}

func DefaultStateDir() (string, error) {
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		if !filepath.IsAbs(stateHome) {
			return "", fmt.Errorf("XDG_STATE_HOME must be an absolute path")
		}

		return filepath.Join(stateHome, ApplicationName), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory for state: %w", err)
	}

	return filepath.Join(home, ".local", "state", ApplicationName), nil
}

func ResolveStateDir(value string) (string, error) {
	if value == "" {
		return DefaultStateDir()
	}

	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("state directory must be an absolute path")
	}

	return filepath.Clean(value), nil
}
