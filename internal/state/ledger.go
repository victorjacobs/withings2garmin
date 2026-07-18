package state

import (
	"fmt"
	"time"
)

type LedgerState string

const (
	LedgerPending    LedgerState = "pending"
	LedgerUploaded   LedgerState = "uploaded"
	LedgerReconciled LedgerState = "reconciled"
	LedgerIgnored    LedgerState = "ignored"
	LedgerConflict   LedgerState = "conflict"
)

func (value LedgerState) Valid() bool {
	switch value {
	case LedgerPending, LedgerUploaded, LedgerReconciled, LedgerIgnored, LedgerConflict:
		return true
	default:
		return false
	}
}

func (value LedgerState) Terminal() bool {
	return value != LedgerPending && value.Valid()
}

type LedgerEntry struct {
	GroupID             int64       `json:"group_id"`
	ObservedFingerprint string      `json:"observed_fingerprint"`
	ObservedMeasuredAt  time.Time   `json:"observed_measured_at"`
	ObservedWeightGrams int64       `json:"observed_weight_grams"`
	SyncedFingerprint   string      `json:"synced_fingerprint,omitempty"`
	SyncedMeasuredAt    *time.Time  `json:"synced_measured_at,omitempty"`
	SyncedWeightGrams   *int64      `json:"synced_weight_grams,omitempty"`
	State               LedgerState `json:"state"`
	GarminSamplePK      *int64      `json:"garmin_sample_pk,omitempty"`
	FirstSeenAt         time.Time   `json:"first_seen_at"`
	LastSeenAt          time.Time   `json:"last_seen_at"`
	Reason              string      `json:"reason,omitempty"`
}

type RunSummary struct {
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	QueryMode     string    `json:"query_mode"`
	Fetched       int       `json:"fetched"`
	Uploaded      int       `json:"uploaded"`
	Reconciled    int       `json:"reconciled"`
	Ignored       int       `json:"ignored"`
	Conflicts     int       `json:"conflicts"`
	NewCursor     int64     `json:"new_cursor,omitempty"`
	ErrorCategory string    `json:"error_category,omitempty"`
}

type SyncState struct {
	SchemaVersion  int                   `json:"schema_version"`
	WithingsCursor int64                 `json:"withings_cursor,omitempty"`
	Ledger         map[int64]LedgerEntry `json:"ledger"`
	LastRun        *RunSummary           `json:"last_run,omitempty"`
}

func NewSyncState() SyncState {
	return SyncState{SchemaVersion: SchemaVersion, Ledger: make(map[int64]LedgerEntry)}
}

func (state SyncState) validate() error {
	if err := validateSchemaVersion(state.SchemaVersion); err != nil {
		return err
	}
	if state.Ledger == nil {
		return fmt.Errorf("state ledger is missing")
	}

	for groupID, entry := range state.Ledger {
		if groupID != entry.GroupID {
			return fmt.Errorf("ledger key %d does not match group ID %d", groupID, entry.GroupID)
		}
		if !entry.State.Valid() {
			return fmt.Errorf("ledger entry %d has invalid state %q", groupID, entry.State)
		}
	}

	return nil
}
