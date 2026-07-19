package app

import (
	"sort"
	"time"

	"github.com/victorjacobs/withings2garmin/internal/state"
	"github.com/victorjacobs/withings2garmin/internal/withings"
)

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
