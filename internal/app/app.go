package app

import (
	"io"
	"log/slog"
	"time"

	"github.com/victorjacobs/withings2garmin/internal/garmin"
	"github.com/victorjacobs/withings2garmin/internal/state"
	"github.com/victorjacobs/withings2garmin/internal/withings"
)

const (
	ExitSuccess  = 0
	ExitCLI      = 2
	ExitReauth   = 3
	ExitConflict = 4
	ExitFailure  = 1
)

type Runtime struct {
	Store    *state.Store
	Withings *withings.Client
	Garmin   *garmin.Client
	Logger   *slog.Logger
	Now      func() time.Time
}

func New(store *state.Store, logger *slog.Logger) (*Runtime, error) {
	garminClient, err := garmin.NewClient(nil, "", "")
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Runtime{
		Store:    store,
		Withings: withings.NewClient(nil),
		Garmin:   garminClient,
		Logger:   logger,
		Now:      time.Now,
	}, nil
}
