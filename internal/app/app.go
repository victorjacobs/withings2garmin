package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
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
	return &Runtime{Store: store, Withings: withings.NewClient(nil), Garmin: garminClient, Logger: logger, Now: time.Now}, nil
}

type SyncOptions struct {
	OAuth            withings.OAuthConfig
	From, To         *time.Time
	InitialLookback  time.Duration
	IncludeAmbiguous bool
	DryRun           bool
	MaxUploads       int
}

type SyncResult struct {
	Fetched, Uploaded, Reconciled, Ignored, Conflicts, WouldUpload int
	Actions                                                        []DryRunAction
}

type DryRunAction struct {
	Action     string
	GroupID    int64
	MeasuredAt time.Time
	Reason     string
}

func (runtime *Runtime) Sync(ctx context.Context, options SyncOptions) (result SyncResult, resultErr error) {
	if options.InitialLookback <= 0 {
		options.InitialLookback = 30 * 24 * time.Hour
	}
	if (options.From == nil) != (options.To == nil) {
		return result, fmt.Errorf("--from and --to must be supplied together")
	}
	if err := runtime.Store.EnsureDirectory(); err != nil {
		return result, err
	}
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lock, err := runtime.Store.AcquireLock(lockCtx)
	if err != nil {
		return result, err
	}
	defer func() {
		if err := lock.Release(); resultErr == nil && err != nil {
			resultErr = err
		}
	}()

	wt, err := runtime.Store.LoadWithingsTokens()
	if err != nil {
		return result, fmt.Errorf("load Withings tokens: %w", err)
	}
	gt, err := runtime.Store.LoadGarminTokens()
	if err != nil {
		return result, fmt.Errorf("load Garmin tokens: %w", err)
	}
	syncState, err := runtime.Store.LoadSyncState()
	if errors.Is(err, state.ErrNotFound) {
		syncState = state.NewSyncState()
	} else if err != nil {
		return result, fmt.Errorf("load sync state: %w", err)
	}

	if wt.ExpiresAt.Before(runtime.Now().Add(5 * time.Minute)) {
		refreshed, err := runtime.Withings.RefreshToken(ctx, options.OAuth, wt.RefreshToken)
		if err != nil {
			return result, fmt.Errorf("refresh Withings token: %w", err)
		}
		wt = withingsStateToken(refreshed)
		if err := runtime.Store.SaveWithingsTokens(wt); err != nil {
			return result, err
		}
	}
	if gt.ExpiresAt.Before(runtime.Now().Add(15 * time.Minute)) {
		refreshed, err := runtime.Garmin.Refresh(ctx, garminToken(gt))
		if err != nil {
			return result, fmt.Errorf("refresh Garmin token: %w", err)
		}
		gt = garminStateToken(refreshed)
		if err := runtime.Store.SaveGarminTokens(gt); err != nil {
			return result, err
		}
	}
	if err := runtime.Garmin.Validate(ctx, gt.AccessToken); err != nil {
		return result, fmt.Errorf("validate Garmin token: %w", err)
	}

	query, backfill := syncQuery(syncState, options, runtime.Now())
	fetched, err := runtime.Withings.FetchMeasurements(ctx, wt.AccessToken, query)
	if errors.Is(err, withings.ErrAuthenticationRequired) {
		refreshed, refreshErr := runtime.Withings.RefreshToken(ctx, options.OAuth, wt.RefreshToken)
		if refreshErr != nil {
			return result, fmt.Errorf("refresh Withings token after authorization failure: %w", refreshErr)
		}
		wt = withingsStateToken(refreshed)
		if saveErr := runtime.Store.SaveWithingsTokens(wt); saveErr != nil {
			return result, saveErr
		}
		fetched, err = runtime.Withings.FetchMeasurements(ctx, wt.AccessToken, query)
	}
	if err != nil {
		return result, err
	}
	result.Fetched = len(fetched.Groups)

	measurements := make([]withings.WeightMeasurement, 0, len(fetched.Groups))
	for _, group := range fetched.Groups {
		decision := withings.FilterAttribution(group.Attribution, options.IncludeAmbiguous)
		if decision != withings.AttributionAccepted {
			if options.DryRun {
				result.Actions = append(result.Actions, DryRunAction{Action: "ignore", GroupID: group.GroupID, MeasuredAt: group.MeasuredAt, Reason: ignoredReason(decision)})
			}
			if !options.DryRun {
				runtime.recordIgnored(&syncState, group, ignoredReason(decision))
				if err := runtime.Store.SaveSyncState(syncState); err != nil {
					return result, err
				}
			}
			result.Ignored++
			continue
		}
		measurement, err := withings.WeightFromGroup(group)
		if err != nil {
			continue
		}
		measurements = append(measurements, measurement)
	}
	sort.Slice(measurements, func(i, j int) bool {
		if measurements[i].MeasuredAt.Equal(measurements[j].MeasuredAt) {
			return measurements[i].WithingsGroupID < measurements[j].WithingsGroupID
		}
		return measurements[i].MeasuredAt.Before(measurements[j].MeasuredAt)
	})

	uploads := 0
	for _, measurement := range measurements {
		fingerprint := measurementFingerprint(measurement)
		if entry, exists := syncState.Ledger[measurement.WithingsGroupID]; exists {
			if entry.ObservedFingerprint == fingerprint && entry.State.Terminal() {
				continue
			}
			if entry.State == state.LedgerPending {
				return result, fmt.Errorf("pending group %d requires operator reconciliation", measurement.WithingsGroupID)
			}
			if entry.State == state.LedgerUploaded || entry.State == state.LedgerReconciled {
				runtime.recordConflict(&syncState, measurement, fingerprint, "source_changed")
				if err := runtime.Store.SaveSyncState(syncState); err != nil {
					return result, err
				}
				result.Conflicts++
				continue
			}
		}
		location, err := time.LoadLocation(measurement.Timezone)
		if err != nil {
			return result, fmt.Errorf("load Withings timezone: %w", err)
		}
		samples, err := runtime.Garmin.DayView(ctx, gt.AccessToken, measurement.MeasuredAt.In(location))
		if err != nil {
			return result, err
		}
		matches, sameTime := 0, false
		for _, sample := range samples {
			if sample.MeasuredAt.UTC().Unix() == measurement.MeasuredAt.UTC().Unix() {
				sameTime = true
				if sample.WeightGrams == measurement.WeightGrams {
					matches++
				}
			}
		}
		if matches == 1 {
			if options.DryRun {
				result.Actions = append(result.Actions, DryRunAction{Action: "reconcile", GroupID: measurement.WithingsGroupID, MeasuredAt: measurement.MeasuredAt, Reason: "remote_match"})
				continue
			}
			runtime.recordTerminal(&syncState, measurement, fingerprint, state.LedgerReconciled, "remote_match")
			if err := runtime.Store.SaveSyncState(syncState); err != nil {
				return result, err
			}
			result.Reconciled++
			continue
		}
		if matches > 1 || sameTime {
			if options.DryRun {
				result.Actions = append(result.Actions, DryRunAction{Action: "conflict", GroupID: measurement.WithingsGroupID, MeasuredAt: measurement.MeasuredAt, Reason: "garmin_timestamp_conflict"})
				continue
			}
			runtime.recordConflict(&syncState, measurement, fingerprint, "garmin_timestamp_conflict")
			if err := runtime.Store.SaveSyncState(syncState); err != nil {
				return result, err
			}
			result.Conflicts++
			continue
		}
		if options.DryRun {
			result.Actions = append(result.Actions, DryRunAction{Action: "upload", GroupID: measurement.WithingsGroupID, MeasuredAt: measurement.MeasuredAt, Reason: "no_remote_match"})
			result.WouldUpload++
			continue
		}
		if options.MaxUploads > 0 && uploads >= options.MaxUploads {
			break
		}
		runtime.recordTerminal(&syncState, measurement, fingerprint, state.LedgerPending, "write_ahead")
		if err := runtime.Store.SaveSyncState(syncState); err != nil {
			return result, err
		}
		if err := runtime.Garmin.UploadWeight(ctx, gt.AccessToken, measurement.MeasuredAt, location, measurement.WeightGrams); err != nil {
			return result, fmt.Errorf("upload Garmin weight: %w", err)
		}
		runtime.recordTerminal(&syncState, measurement, fingerprint, state.LedgerUploaded, "uploaded")
		if err := runtime.Store.SaveSyncState(syncState); err != nil {
			return result, err
		}
		uploads++
		result.Uploaded++
	}
	if !options.DryRun && !backfill && result.Conflicts == 0 && !fetched.UpdateTime.IsZero() {
		syncState.WithingsCursor = fetched.UpdateTime.Unix()
		if err := runtime.Store.SaveSyncState(syncState); err != nil {
			return result, err
		}
	}
	return result, nil
}

func ignoredReason(decision withings.AttributionDecision) string {
	switch decision {
	case withings.AttributionManual:
		return "manual_attribution"
	case withings.AttributionAmbiguous:
		return "ambiguous_attribution"
	default:
		return "unknown_attribution"
	}
}

func syncQuery(syncState state.SyncState, options SyncOptions, now time.Time) (withings.Query, bool) {
	if options.From != nil {
		return withings.Query{StartDate: options.From, EndDate: options.To}, true
	}
	if syncState.WithingsCursor > 0 {
		cursor := time.Unix(syncState.WithingsCursor-1, 0).UTC()
		return withings.Query{LastUpdate: &cursor}, false
	}
	start := now.Add(-options.InitialLookback).UTC()
	end := now.UTC()
	return withings.Query{StartDate: &start, EndDate: &end}, false
}
