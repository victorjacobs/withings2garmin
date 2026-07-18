package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/victorjacobs/withings2garmin/internal/state"
	"github.com/victorjacobs/withings2garmin/internal/withings"
)

const (
	withingsRefreshWindow = 5 * time.Minute
	garminRefreshWindow   = 15 * time.Minute
	lockTimeout           = 10 * time.Second
)

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

func (runtime *Runtime) prepareTokens(
	ctx context.Context,
	oauth withings.OAuthConfig,
	withingsTokens state.WithingsTokens,
	garminTokens state.GarminTokens,
) (state.WithingsTokens, state.GarminTokens, error) {
	var err error

	withingsTokens, err = runtime.refreshWithingsIfNeeded(ctx, oauth, withingsTokens)
	if err != nil {
		return state.WithingsTokens{}, state.GarminTokens{}, err
	}

	garminTokens, err = runtime.refreshGarminIfNeeded(ctx, garminTokens)
	if err != nil {
		return state.WithingsTokens{}, state.GarminTokens{}, err
	}

	if err := runtime.Garmin.Validate(ctx, garminTokens.AccessToken); err != nil {
		return state.WithingsTokens{}, state.GarminTokens{}, fmt.Errorf(
			"validate Garmin token: %w",
			err,
		)
	}

	return withingsTokens, garminTokens, nil
}

func (runtime *Runtime) refreshWithingsIfNeeded(
	ctx context.Context,
	oauth withings.OAuthConfig,
	tokens state.WithingsTokens,
) (state.WithingsTokens, error) {
	if tokens.ExpiresAt.After(runtime.Now().Add(withingsRefreshWindow)) {
		return tokens, nil
	}

	refreshed, err := runtime.Withings.RefreshToken(ctx, oauth, tokens.RefreshToken)
	if err != nil {
		return state.WithingsTokens{}, fmt.Errorf("refresh Withings token: %w", err)
	}

	tokens = withingsStateToken(refreshed)
	if err := runtime.Store.SaveWithingsTokens(tokens); err != nil {
		return state.WithingsTokens{}, err
	}

	return tokens, nil
}

func (runtime *Runtime) refreshGarminIfNeeded(
	ctx context.Context,
	tokens state.GarminTokens,
) (state.GarminTokens, error) {
	if tokens.ExpiresAt.After(runtime.Now().Add(garminRefreshWindow)) {
		return tokens, nil
	}

	refreshed, err := runtime.Garmin.Refresh(ctx, garminToken(tokens))
	if err != nil {
		return state.GarminTokens{}, fmt.Errorf("refresh Garmin token: %w", err)
	}

	tokens = garminStateToken(refreshed)
	if err := runtime.Store.SaveGarminTokens(tokens); err != nil {
		return state.GarminTokens{}, err
	}

	return tokens, nil
}

func (runtime *Runtime) fetchMeasurements(
	ctx context.Context,
	oauth withings.OAuthConfig,
	tokens state.WithingsTokens,
	query withings.Query,
) (withings.FetchResult, error) {
	fetched, err := runtime.Withings.FetchMeasurements(ctx, tokens.AccessToken, query)
	if !errors.Is(err, withings.ErrAuthenticationRequired) {
		return fetched, err
	}

	refreshed, refreshErr := runtime.Withings.RefreshToken(ctx, oauth, tokens.RefreshToken)
	if refreshErr != nil {
		return withings.FetchResult{}, fmt.Errorf(
			"refresh Withings token after authorization failure: %w",
			refreshErr,
		)
	}

	tokens = withingsStateToken(refreshed)
	if err := runtime.Store.SaveWithingsTokens(tokens); err != nil {
		return withings.FetchResult{}, err
	}

	return runtime.Withings.FetchMeasurements(ctx, tokens.AccessToken, query)
}

func (runtime *Runtime) normalizedMeasurements(
	result *SyncResult,
	syncState *state.SyncState,
	groups []withings.MeasureGroup,
	options SyncOptions,
) ([]withings.WeightMeasurement, error) {
	measurements := make([]withings.WeightMeasurement, 0, len(groups))
	for _, group := range groups {
		if err := runtime.normalizeGroup(result, syncState, group, options, &measurements); err != nil {
			return nil, err
		}
	}

	sort.Slice(measurements, func(i, j int) bool {
		if measurements[i].MeasuredAt.Equal(measurements[j].MeasuredAt) {
			return measurements[i].WithingsGroupID < measurements[j].WithingsGroupID
		}

		return measurements[i].MeasuredAt.Before(measurements[j].MeasuredAt)
	})

	return measurements, nil
}

func (runtime *Runtime) normalizeGroup(
	result *SyncResult,
	syncState *state.SyncState,
	group withings.MeasureGroup,
	options SyncOptions,
	measurements *[]withings.WeightMeasurement,
) error {
	decision := withings.FilterAttribution(group.Attribution, options.IncludeAmbiguous)
	if decision != withings.AttributionAccepted {
		return runtime.recordIgnoredGroup(result, syncState, group, options.DryRun, decision)
	}

	measurement, err := withings.WeightFromGroup(group)
	if err != nil {
		return nil
	}

	*measurements = append(*measurements, measurement)

	return nil
}

func (runtime *Runtime) recordIgnoredGroup(
	result *SyncResult,
	syncState *state.SyncState,
	group withings.MeasureGroup,
	dryRun bool,
	decision withings.AttributionDecision,
) error {
	reason := ignoredReason(decision)
	if dryRun {
		result.Actions = append(result.Actions, DryRunAction{
			Action:     "ignore",
			GroupID:    group.GroupID,
			MeasuredAt: group.MeasuredAt,
			Reason:     reason,
		})
	} else {
		runtime.recordIgnored(syncState, group, reason)
		if err := runtime.Store.SaveSyncState(*syncState); err != nil {
			return err
		}
	}

	result.Ignored++

	return nil
}

func (runtime *Runtime) syncMeasurements(
	ctx context.Context,
	result *SyncResult,
	syncState *state.SyncState,
	accessToken string,
	measurements []withings.WeightMeasurement,
	options SyncOptions,
) error {
	uploads := 0
	for _, measurement := range measurements {
		if options.MaxUploads > 0 && uploads >= options.MaxUploads {
			break
		}

		uploaded, err := runtime.syncMeasurement(
			ctx,
			result,
			syncState,
			accessToken,
			measurement,
			options.DryRun,
		)
		if err != nil {
			return err
		}
		if uploaded {
			uploads++
		}
	}

	return nil
}

func (runtime *Runtime) syncMeasurement(
	ctx context.Context,
	result *SyncResult,
	syncState *state.SyncState,
	accessToken string,
	measurement withings.WeightMeasurement,
	dryRun bool,
) (bool, error) {
	fingerprint := measurementFingerprint(measurement)
	if handled, err := runtime.handleExistingEntry(result, syncState, measurement, fingerprint); handled || err != nil {
		return false, err
	}

	location, err := time.LoadLocation(measurement.Timezone)
	if err != nil {
		return false, fmt.Errorf("load Withings timezone: %w", err)
	}

	matches, sameTime, err := runtime.remoteMatches(ctx, accessToken, measurement, location)
	if err != nil {
		return false, err
	}

	return runtime.handleRemoteMatch(
		ctx,
		result,
		syncState,
		accessToken,
		measurement,
		fingerprint,
		location,
		matches,
		sameTime,
		dryRun,
	)
}

func (runtime *Runtime) handleExistingEntry(
	result *SyncResult,
	syncState *state.SyncState,
	measurement withings.WeightMeasurement,
	fingerprint string,
) (bool, error) {
	entry, exists := syncState.Ledger[measurement.WithingsGroupID]
	if !exists {
		return false, nil
	}
	if entry.ObservedFingerprint == fingerprint && entry.State.Terminal() {
		return true, nil
	}
	if entry.State == state.LedgerPending {
		return true, fmt.Errorf(
			"pending group %d requires operator reconciliation",
			measurement.WithingsGroupID,
		)
	}
	if entry.State != state.LedgerUploaded && entry.State != state.LedgerReconciled {
		return false, nil
	}

	runtime.recordConflict(syncState, measurement, fingerprint, "source_changed")
	if err := runtime.Store.SaveSyncState(*syncState); err != nil {
		return true, err
	}

	result.Conflicts++

	return true, nil
}

func (runtime *Runtime) remoteMatches(
	ctx context.Context,
	accessToken string,
	measurement withings.WeightMeasurement,
	location *time.Location,
) (int, bool, error) {
	samples, err := runtime.Garmin.DayView(
		ctx,
		accessToken,
		measurement.MeasuredAt.In(location),
	)
	if err != nil {
		return 0, false, err
	}

	matches := 0
	sameTime := false
	for _, sample := range samples {
		if sample.MeasuredAt.UTC().Unix() != measurement.MeasuredAt.UTC().Unix() {
			continue
		}

		sameTime = true
		if sample.WeightGrams == measurement.WeightGrams {
			matches++
		}
	}

	return matches, sameTime, nil
}

func (runtime *Runtime) handleRemoteMatch(
	ctx context.Context,
	result *SyncResult,
	syncState *state.SyncState,
	accessToken string,
	measurement withings.WeightMeasurement,
	fingerprint string,
	location *time.Location,
	matches int,
	sameTime bool,
	dryRun bool,
) (bool, error) {
	if matches == 1 {
		return runtime.recordReconciliation(result, syncState, measurement, fingerprint, dryRun)
	}
	if matches > 1 || sameTime {
		return runtime.recordTimestampConflict(result, syncState, measurement, fingerprint, dryRun)
	}
	if dryRun {
		result.Actions = append(result.Actions, DryRunAction{
			Action:     "upload",
			GroupID:    measurement.WithingsGroupID,
			MeasuredAt: measurement.MeasuredAt,
			Reason:     "no_remote_match",
		})
		result.WouldUpload++

		return false, nil
	}

	return runtime.uploadMeasurement(
		ctx,
		result,
		syncState,
		accessToken,
		measurement,
		fingerprint,
		location,
	)
}

func (runtime *Runtime) recordReconciliation(
	result *SyncResult,
	syncState *state.SyncState,
	measurement withings.WeightMeasurement,
	fingerprint string,
	dryRun bool,
) (bool, error) {
	if dryRun {
		result.Actions = append(result.Actions, DryRunAction{
			Action:     "reconcile",
			GroupID:    measurement.WithingsGroupID,
			MeasuredAt: measurement.MeasuredAt,
			Reason:     "remote_match",
		})

		return false, nil
	}

	runtime.recordTerminal(syncState, measurement, fingerprint, state.LedgerReconciled, "remote_match")
	if err := runtime.Store.SaveSyncState(*syncState); err != nil {
		return false, err
	}

	result.Reconciled++

	return false, nil
}

func (runtime *Runtime) recordTimestampConflict(
	result *SyncResult,
	syncState *state.SyncState,
	measurement withings.WeightMeasurement,
	fingerprint string,
	dryRun bool,
) (bool, error) {
	if dryRun {
		result.Actions = append(result.Actions, DryRunAction{
			Action:     "conflict",
			GroupID:    measurement.WithingsGroupID,
			MeasuredAt: measurement.MeasuredAt,
			Reason:     "garmin_timestamp_conflict",
		})

		return false, nil
	}

	runtime.recordConflict(syncState, measurement, fingerprint, "garmin_timestamp_conflict")
	if err := runtime.Store.SaveSyncState(*syncState); err != nil {
		return false, err
	}

	result.Conflicts++

	return false, nil
}

func (runtime *Runtime) uploadMeasurement(
	ctx context.Context,
	result *SyncResult,
	syncState *state.SyncState,
	accessToken string,
	measurement withings.WeightMeasurement,
	fingerprint string,
	location *time.Location,
) (bool, error) {
	runtime.recordTerminal(syncState, measurement, fingerprint, state.LedgerPending, "write_ahead")
	if err := runtime.Store.SaveSyncState(*syncState); err != nil {
		return false, err
	}

	if err := runtime.Garmin.UploadWeight(
		ctx,
		accessToken,
		measurement.MeasuredAt,
		location,
		measurement.WeightGrams,
	); err != nil {
		return false, fmt.Errorf("upload Garmin weight: %w", err)
	}

	runtime.recordTerminal(syncState, measurement, fingerprint, state.LedgerUploaded, "uploaded")
	if err := runtime.Store.SaveSyncState(*syncState); err != nil {
		return false, err
	}

	result.Uploaded++

	return true, nil
}

func (runtime *Runtime) commitCursor(
	syncState *state.SyncState,
	fetched withings.FetchResult,
	options SyncOptions,
	backfill bool,
	result SyncResult,
) error {
	if options.DryRun || backfill || result.Conflicts > 0 || fetched.UpdateTime.IsZero() {
		return nil
	}

	syncState.WithingsCursor = fetched.UpdateTime.Unix()

	return runtime.Store.SaveSyncState(*syncState)
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
		return withings.Query{
			StartDate: options.From,
			EndDate:   options.To,
		}, true
	}
	if syncState.WithingsCursor > 0 {
		cursor := time.Unix(syncState.WithingsCursor-1, 0).UTC()

		return withings.Query{LastUpdate: &cursor}, false
	}

	start := now.Add(-options.InitialLookback).UTC()
	end := now.UTC()

	return withings.Query{
		StartDate: &start,
		EndDate:   &end,
	}, false
}
