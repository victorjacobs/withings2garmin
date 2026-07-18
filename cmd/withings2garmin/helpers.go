package main

import (
	"errors"
	"log/slog"
	"time"

	"github.com/spf13/cobra"
	"github.com/victorjacobs/withings2garmin/internal/app"
	"github.com/victorjacobs/withings2garmin/internal/config"
	"github.com/victorjacobs/withings2garmin/internal/garmin"
	"github.com/victorjacobs/withings2garmin/internal/secret"
	"github.com/victorjacobs/withings2garmin/internal/state"
	"github.com/victorjacobs/withings2garmin/internal/withings"
)

func parseBackfillRange(from, to string) (*time.Time, *time.Time, error) {
	if from == "" && to == "" {
		return nil, nil, nil
	}
	if from == "" || to == "" {
		return nil, nil, errors.New("--from and --to must be supplied together")
	}
	start, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil, nil, err
	}
	end, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil, nil, err
	}
	end = end.Add(24*time.Hour - time.Second)
	return &start, &end, nil
}

func optionalSecret(path, purpose string) (string, error) {
	if path == "" {
		return "", nil
	}
	value, err := secret.ReadFile(path, purpose)
	return string(value), err
}

func withingsStateToken(t withings.Token) state.WithingsTokens {
	return state.WithingsTokens{
		SchemaVersion: 1,
		UserID:        t.UserID,
		AccessToken:   t.AccessToken,
		RefreshToken:  t.RefreshToken,
		Scope:         t.Scope,
		TokenType:     t.TokenType,
		ObtainedAt:    t.ObtainedAt,
		ExpiresAt:     t.ExpiresAt,
	}
}

func garminStateToken(t garmin.TokenSet) state.GarminTokens {
	return state.GarminTokens{
		SchemaVersion: 1,
		AccessToken:   t.AccessToken,
		RefreshToken:  t.RefreshToken,
		ClientID:      t.ClientID,
		ExpiresAt:     t.ExpiresAt,
		ObtainedAt:    time.Now().UTC(),
	}
}

func cliError(err error) error { return &commandError{exitCode: app.ExitCLI, err: err} }

func operationalError(err error) error {
	if err == nil {
		return nil
	}
	return &commandError{exitCode: app.ExitFailure, err: err}
}

func classifiedError(err error) error {
	if errors.Is(err, garmin.ErrAuthenticationRequired) || errors.Is(err, withings.ErrAuthenticationRequired) {
		return &commandError{exitCode: app.ExitReauth, err: err}
	}
	return operationalError(err)
}

func markFlagRequired(c *cobra.Command, n string) {
	if err := c.MarkFlagRequired(n); err != nil {
		panic(err)
	}
}

func levelToSlog(l config.LogLevel) slog.Level {
	switch l {
	case config.LogLevelDebug:
		return slog.LevelDebug
	case config.LogLevelWarn:
		return slog.LevelWarn
	case config.LogLevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
