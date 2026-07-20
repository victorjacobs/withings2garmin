package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/victorjacobs/garmin-import/internal/state"
	"github.com/victorjacobs/garmin-import/internal/withings"
)

const lockTimeout = 10 * time.Second

type SyncOptions struct {
	OAuth            withings.OAuthConfig
	From, To         *time.Time
	InitialLookback  time.Duration
	IncludeAmbiguous bool
	DryRun           bool
	MaxUploads       int
}

type SyncResult struct {
	Fetched     int
	Uploaded    int
	Reconciled  int
	Ignored     int
	Conflicts   int
	WouldUpload int
	Actions     []DryRunAction
}

type DryRunAction struct {
	Action     string
	GroupID    int64
	MeasuredAt time.Time
	Reason     string
}

func (runtime *Runtime) Sync(
	ctx context.Context,
	options SyncOptions,
) (result SyncResult, resultErr error) {
	if err := options.validate(); err != nil {
		return result, err
	}

	lock, err := runtime.acquireSyncLock(ctx)
	if err != nil {
		return result, err
	}
	defer func() {
		if err := lock.Release(); resultErr == nil && err != nil {
			resultErr = err
		}
	}()

	withingsTokens, garminTokens, syncState, err := runtime.loadSyncInputs()
	if err != nil {
		return result, err
	}

	withingsTokens, garminTokens, err = runtime.prepareTokens(
		ctx,
		options.OAuth,
		withingsTokens,
		garminTokens,
	)
	if err != nil {
		return result, err
	}

	query, backfill := syncQuery(syncState, options, runtime.Now())
	fetched, err := runtime.fetchMeasurements(ctx, options.OAuth, withingsTokens, query)
	if err != nil {
		return result, err
	}
	result.Fetched = len(fetched.Groups)

	measurements, err := runtime.normalizedMeasurements(
		&result,
		&syncState,
		fetched.Groups,
		options,
	)
	if err != nil {
		return result, err
	}

	if err := runtime.syncMeasurements(
		ctx,
		&result,
		&syncState,
		garminTokens.AccessToken,
		measurements,
		options,
	); err != nil {
		return result, err
	}

	if err := runtime.commitCursor(&syncState, fetched, options, backfill, result); err != nil {
		return result, err
	}

	return result, nil
}

func (options *SyncOptions) validate() error {
	if options.InitialLookback <= 0 {
		options.InitialLookback = 30 * 24 * time.Hour
	}
	if (options.From == nil) != (options.To == nil) {
		return errors.New("--from and --to must be supplied together")
	}

	return nil
}

func (runtime *Runtime) acquireSyncLock(ctx context.Context) (*state.Lock, error) {
	if err := runtime.Store.EnsureDirectory(); err != nil {
		return nil, err
	}

	lockCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()

	return runtime.Store.AcquireLock(lockCtx)
}

func (runtime *Runtime) loadSyncInputs() (
	state.WithingsTokens,
	state.GarminTokens,
	state.SyncState,
	error,
) {
	withingsTokens, err := runtime.Store.LoadWithingsTokens()
	if err != nil {
		return state.WithingsTokens{}, state.GarminTokens{}, state.SyncState{}, fmt.Errorf(
			"load Withings tokens: %w",
			err,
		)
	}

	garminTokens, err := runtime.Store.LoadGarminTokens()
	if err != nil {
		return state.WithingsTokens{}, state.GarminTokens{}, state.SyncState{}, fmt.Errorf(
			"load Garmin tokens: %w",
			err,
		)
	}

	syncState, err := runtime.Store.LoadSyncState()
	if errors.Is(err, state.ErrNotFound) {
		return withingsTokens, garminTokens, state.NewSyncState(), nil
	}
	if err != nil {
		return state.WithingsTokens{}, state.GarminTokens{}, state.SyncState{}, fmt.Errorf(
			"load sync state: %w",
			err,
		)
	}

	return withingsTokens, garminTokens, syncState, nil
}
