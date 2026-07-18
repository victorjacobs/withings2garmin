package garmin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

type WeightSample struct {
	MeasuredAt  time.Time
	WeightGrams int64
	SamplePK    string
}

type uploadWeightRequest struct {
	DateTimestamp string      `json:"dateTimestamp"`
	GMTTimestamp  string      `json:"gmtTimestamp"`
	UnitKey       string      `json:"unitKey"`
	SourceType    string      `json:"sourceType"`
	Value         json.Number `json:"value"`
}

func marshalWeightUpload(measuredAt time.Time, location *time.Location, grams int64) ([]byte, error) {
	if grams < 500 || grams > 500000 {
		return nil, fmt.Errorf("weight %d grams outside supported range", grams)
	}
	if location == nil {
		return nil, fmt.Errorf("garmin local timezone is required")
	}

	value := strconv.FormatInt(grams/1000, 10)
	if remainder := grams % 1000; remainder != 0 {
		value += "." + fmt.Sprintf("%03d", remainder)
		for value[len(value)-1] == '0' {
			value = value[:len(value)-1]
		}
	}

	payload := uploadWeightRequest{
		DateTimestamp: measuredAt.In(location).Format("2006-01-02T15:04:05.000"),
		GMTTimestamp:  measuredAt.UTC().Format("2006-01-02T15:04:05.000"),
		UnitKey:       "kg",
		SourceType:    "MANUAL",
		Value:         json.Number(value),
	}

	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, fmt.Errorf("encode Garmin weight request: %w", err)
	}

	return body.Bytes(), nil
}
