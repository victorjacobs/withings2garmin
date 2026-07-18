package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/victorjacobs/withings2garmin/internal/garmin"
	"github.com/victorjacobs/withings2garmin/internal/state"
	"github.com/victorjacobs/withings2garmin/internal/withings"
)

func measurementFingerprint(measurement withings.WeightMeasurement) string {
	value := fmt.Sprintf(
		"%d:%d:%d:%d:%d:%s",
		measurement.WithingsGroupID,
		measurement.ModifiedAt.Unix(),
		measurement.MeasuredAt.Unix(),
		measurement.WeightGrams,
		measurement.Attribution,
		measurement.DeviceID,
	)
	sum := sha256.Sum256([]byte(value))

	return hex.EncodeToString(sum[:])
}

func (runtime *Runtime) recordIgnored(syncState *state.SyncState, group withings.MeasureGroup, reason string) {
	now := runtime.Now().UTC()
	syncState.Ledger[group.GroupID] = state.LedgerEntry{
		GroupID:             group.GroupID,
		ObservedFingerprint: fmt.Sprintf("ignored:%d:%d", group.ModifiedAt.Unix(), group.Attribution),
		State:               state.LedgerIgnored,
		FirstSeenAt:         now,
		LastSeenAt:          now,
		Reason:              reason,
	}
}

func (runtime *Runtime) recordConflict(syncState *state.SyncState, measurement withings.WeightMeasurement, fingerprint, reason string) {
	runtime.recordTerminal(syncState, measurement, fingerprint, state.LedgerConflict, reason)
}

func (runtime *Runtime) recordTerminal(
	syncState *state.SyncState,
	measurement withings.WeightMeasurement,
	fingerprint string,
	status state.LedgerState,
	reason string,
) {
	now := runtime.Now().UTC()
	previous := syncState.Ledger[measurement.WithingsGroupID]
	firstSeenAt := previous.FirstSeenAt
	if firstSeenAt.IsZero() {
		firstSeenAt = now
	}

	syncState.Ledger[measurement.WithingsGroupID] = state.LedgerEntry{
		GroupID:             measurement.WithingsGroupID,
		ObservedFingerprint: fingerprint,
		ObservedMeasuredAt:  measurement.MeasuredAt,
		ObservedWeightGrams: measurement.WeightGrams,
		SyncedFingerprint:   fingerprint,
		State:               status,
		FirstSeenAt:         firstSeenAt,
		LastSeenAt:          now,
		Reason:              reason,
	}
}

func withingsStateToken(token withings.Token) state.WithingsTokens {
	return state.WithingsTokens{
		SchemaVersion: 1,
		UserID:        token.UserID,
		AccessToken:   token.AccessToken,
		RefreshToken:  token.RefreshToken,
		Scope:         token.Scope,
		TokenType:     token.TokenType,
		ObtainedAt:    token.ObtainedAt,
		ExpiresAt:     token.ExpiresAt,
	}
}

func garminToken(token state.GarminTokens) garmin.TokenSet {
	return garmin.TokenSet{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ClientID:     token.ClientID,
		ExpiresAt:    token.ExpiresAt,
	}
}

func garminStateToken(token garmin.TokenSet) state.GarminTokens {
	return state.GarminTokens{
		SchemaVersion: 1,
		AccessToken:   token.AccessToken,
		RefreshToken:  token.RefreshToken,
		ClientID:      token.ClientID,
		ExpiresAt:     token.ExpiresAt,
		ObtainedAt:    time.Now().UTC(),
	}
}
