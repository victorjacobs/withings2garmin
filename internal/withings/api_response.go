package withings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

type measureEnvelope struct {
	Status int            `json:"status"`
	Body   rawMeasureBody `json:"body"`
}

type rawMeasureBody struct {
	MeasureGroups []rawMeasureGroup `json:"measuregrps"`
	More          int               `json:"more"`
	Offset        json.Number       `json:"offset"`
	UpdateTime    int64             `json:"updatetime"`
}

func (body rawMeasureBody) groups() ([]MeasureGroup, error) {
	groups := make([]MeasureGroup, 0, len(body.MeasureGroups))
	for _, rawGroup := range body.MeasureGroups {
		group, err := rawGroup.group()
		if err != nil {
			return nil, err
		}

		groups = append(groups, group)
	}

	return groups, nil
}

type rawMeasureGroup struct {
	GroupID  int64           `json:"grpid"`
	Category int             `json:"category"`
	Date     int64           `json:"date"`
	Created  int64           `json:"created"`
	Modified int64           `json:"modified"`
	Attrib   int             `json:"attrib"`
	DeviceID json.RawMessage `json:"deviceid"`
	Model    json.RawMessage `json:"model"`
	Timezone string          `json:"timezone"`
	Measures []rawMeasure    `json:"measures"`
}

type rawMeasure struct {
	Type  int64       `json:"type"`
	Value json.Number `json:"value"`
	Unit  int         `json:"unit"`
}

func (raw rawMeasureGroup) group() (MeasureGroup, error) {
	if raw.GroupID == 0 || raw.Date <= 0 {
		return MeasureGroup{}, fmt.Errorf("%w: malformed measurement group", ErrProtocol)
	}

	measures := make([]Measure, 0, len(raw.Measures))
	for _, rawMeasure := range raw.Measures {
		value, err := rawMeasure.Value.Int64()
		if err != nil {
			return MeasureGroup{}, fmt.Errorf("%w: measurement value is not an integer", ErrProtocol)
		}

		measures = append(measures, Measure{Type: rawMeasure.Type, Value: value, Unit: rawMeasure.Unit})
	}

	deviceID, err := rawString(raw.DeviceID)
	if err != nil {
		return MeasureGroup{}, err
	}

	model, err := rawString(raw.Model)
	if err != nil {
		return MeasureGroup{}, err
	}

	return MeasureGroup{
		GroupID:     raw.GroupID,
		Category:    raw.Category,
		MeasuredAt:  time.Unix(raw.Date, 0).UTC(),
		CreatedAt:   time.Unix(raw.Created, 0).UTC(),
		ModifiedAt:  time.Unix(raw.Modified, 0).UTC(),
		Attribution: raw.Attrib,
		DeviceID:    deviceID,
		Model:       model,
		Timezone:    raw.Timezone,
		Measures:    measures,
	}, nil
}

func rawString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}

	var stringValue string
	if err := json.Unmarshal(raw, &stringValue); err == nil {
		return stringValue, nil
	}

	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return number.String(), nil
	}

	return "", fmt.Errorf("%w: expected string-compatible group field", ErrProtocol)
}
