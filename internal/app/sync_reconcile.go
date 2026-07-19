package app

import (
	"context"
	"fmt"
	"time"

	"github.com/victorjacobs/withings2garmin/internal/state"
	"github.com/victorjacobs/withings2garmin/internal/withings"
)

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
