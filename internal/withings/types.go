package withings

import (
	"fmt"
	"math"
	"time"
)

const (
	WeightMeasureType = 1
	RealCategory      = 1
	MinimumWeightGram = int64(500)
	MaximumWeightGram = int64(500000)
)

// Token is the persisted Withings OAuth token set. Tokens must never be logged.
type Token struct {
	SchemaVersion int       `json:"schema_version"`
	UserID        string    `json:"user_id"`
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token"`
	Scope         string    `json:"scope"`
	TokenType     string    `json:"token_type"`
	ObtainedAt    time.Time `json:"obtained_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

func (token Token) NeedsRefresh(now time.Time) bool {
	return token.AccessToken == "" || !token.ExpiresAt.After(now.Add(5*time.Minute))
}

type Measure struct {
	Type  int64
	Value int64
	Unit  int
}

type MeasureGroup struct {
	GroupID     int64
	Category    int
	MeasuredAt  time.Time
	CreatedAt   time.Time
	ModifiedAt  time.Time
	Attribution int
	DeviceID    string
	Model       string
	Timezone    string
	Measures    []Measure
}

// WeightMeasurement is the normalized, exact domain representation.
type WeightMeasurement struct {
	WithingsGroupID int64
	MeasuredAt      time.Time
	ModifiedAt      time.Time
	WeightGrams     int64
	Attribution     int
	DeviceID        string
	Model           string
	Timezone        string
}

type Query struct {
	StartDate  *time.Time
	EndDate    *time.Time
	LastUpdate *time.Time
}

func (query Query) validate() error {
	if query.LastUpdate != nil && (query.StartDate != nil || query.EndDate != nil) {
		return fmt.Errorf("%w: lastupdate cannot be combined with a date range", ErrProtocol)
	}
	if (query.StartDate == nil) != (query.EndDate == nil) {
		return fmt.Errorf("%w: startdate and enddate must be supplied together", ErrProtocol)
	}
	if query.StartDate != nil && query.EndDate.Before(*query.StartDate) {
		return fmt.Errorf("%w: enddate precedes startdate", ErrProtocol)
	}
	return nil
}

type FetchResult struct {
	Groups     []MeasureGroup
	UpdateTime time.Time
	PageCount  int
	NoData     bool
}

type AttributionDecision uint8

const (
	AttributionAccepted AttributionDecision = iota
	AttributionManual
	AttributionAmbiguous
	AttributionUnknown
)

func FilterAttribution(attribution int, includeAmbiguous bool) AttributionDecision {
	switch attribution {
	case 0, 8:
		return AttributionAccepted
	case 2, 4:
		return AttributionManual
	case 1:
		if includeAmbiguous {
			return AttributionAccepted
		}
		return AttributionAmbiguous
	default:
		return AttributionUnknown
	}
}

// WeightFromGroup extracts the sole weight measure and converts it to exact grams.
func WeightFromGroup(group MeasureGroup) (WeightMeasurement, error) {
	if group.Category != RealCategory {
		return WeightMeasurement{}, fmt.Errorf("%w: group %d is not a real measurement", ErrInvalidMeasurement, group.GroupID)
	}

	var weight *Measure
	for index := range group.Measures {
		if group.Measures[index].Type != WeightMeasureType {
			continue
		}
		if weight != nil {
			return WeightMeasurement{}, fmt.Errorf(
				"%w: group %d has multiple weight measures",
				ErrInvalidMeasurement,
				group.GroupID,
			)
		}
		weight = &group.Measures[index]
	}
	if weight == nil {
		return WeightMeasurement{}, fmt.Errorf("%w: group %d has no weight measure", ErrInvalidMeasurement, group.GroupID)
	}

	grams, err := gramsFromValue(weight.Value, weight.Unit)
	if err != nil {
		return WeightMeasurement{}, fmt.Errorf("group %d: %w", group.GroupID, err)
	}
	if grams < MinimumWeightGram || grams > MaximumWeightGram {
		return WeightMeasurement{}, fmt.Errorf(
			"%w: group %d is outside the supported range",
			ErrInvalidMeasurement,
			group.GroupID,
		)
	}

	return WeightMeasurement{
		WithingsGroupID: group.GroupID,
		MeasuredAt:      group.MeasuredAt.UTC(),
		ModifiedAt:      group.ModifiedAt.UTC(),
		WeightGrams:     grams,
		Attribution:     group.Attribution,
		DeviceID:        group.DeviceID,
		Model:           group.Model,
		Timezone:        group.Timezone,
	}, nil
}

// gramsFromValue uses round-half-up when Withings' exponent requires a division.
// Values are non-negative: a half of a gram is therefore always rounded upward.
func gramsFromValue(value int64, unit int) (int64, error) {
	if value < 0 {
		return 0, fmt.Errorf("%w: negative weight", ErrInvalidMeasurement)
	}
	exponent := unit + 3
	if exponent >= 0 {
		factor, ok := power10(exponent)
		if !ok || (value != 0 && value > math.MaxInt64/factor) {
			return 0, fmt.Errorf("%w: weight overflows grams", ErrInvalidMeasurement)
		}
		return value * factor, nil
	}

	factor, ok := power10(-exponent)
	if !ok {
		return 0, fmt.Errorf("%w: unsupported weight exponent", ErrInvalidMeasurement)
	}
	quotient := value / factor
	remainder := value % factor
	if remainder >= (factor+1)/2 {
		if quotient == math.MaxInt64 {
			return 0, fmt.Errorf("%w: weight overflows grams", ErrInvalidMeasurement)
		}
		quotient++
	}
	return quotient, nil
}

func power10(exponent int) (int64, bool) {
	if exponent < 0 {
		return 0, false
	}
	value := int64(1)
	for range exponent {
		if value > math.MaxInt64/10 {
			return 0, false
		}
		value *= 10
	}
	return value, true
}
