package garmin

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 4 << 20

func readBounded(body io.Reader) ([]byte, error) {
	result, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Garmin response: %w", err)
	}
	if len(result) > maxResponseBytes {
		return nil, fmt.Errorf("garmin response exceeds %d bytes: %w", maxResponseBytes, ErrProtocol)
	}

	return result, nil
}

func decodeWeightSamples(body []byte) ([]WeightSample, error) {
	var document any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}

	return findSamples(document), nil
}

func findSamples(value any) []WeightSample {
	var found []WeightSample

	var walk func(any)
	walk = func(current any) {
		switch node := current.(type) {
		case []any:
			for _, item := range node {
				walk(item)
			}
		case map[string]any:
			if sample, ok := parseSample(node); ok {
				found = append(found, sample)

				return
			}
			for _, item := range node {
				walk(item)
			}
		}
	}
	walk(value)

	return found
}

func parseSample(record map[string]any) (WeightSample, bool) {
	value, hasValue := record["weight"]
	if !hasValue {
		value, hasValue = record["value"]
	}
	if !hasValue {
		return WeightSample{}, false
	}

	grams, ok := decimalKilogramsToGrams(value)
	if !ok {
		return WeightSample{}, false
	}

	var timestamp time.Time
	for _, key := range []string{"gmtTimestamp", "dateTimestamp", "timestamp"} {
		if raw, exists := record[key]; exists {
			timestamp, ok = parseTimestamp(raw)
			if ok {
				break
			}
		}
	}
	if !ok {
		return WeightSample{}, false
	}

	sample := WeightSample{MeasuredAt: timestamp.UTC(), WeightGrams: grams}
	if raw, exists := record["samplePk"]; exists {
		sample.SamplePK = fmt.Sprint(raw)
	}

	return sample, true
}

func parseTimestamp(value any) (time.Time, bool) {
	switch raw := value.(type) {
	case json.Number:
		seconds, err := raw.Int64()
		if err == nil {
			return time.Unix(seconds, 0), true
		}
	case string:
		for _, layout := range []string{"2006-01-02T15:04:05.000", time.RFC3339, "2006-01-02T15:04:05"} {
			if parsed, err := time.Parse(layout, raw); err == nil {
				return parsed, true
			}
		}
	}

	return time.Time{}, false
}

func decimalKilogramsToGrams(value any) (int64, bool) {
	var raw string
	switch typed := value.(type) {
	case json.Number:
		raw = typed.String()
	case string:
		raw = typed
	default:
		return 0, false
	}
	if strings.HasPrefix(raw, "-") {
		return 0, false
	}

	parts := strings.Split(raw, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, false
	}

	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, false
	}
	if whole > (1<<63-1)/1000 {
		return 0, false
	}

	grams := whole * 1000
	if len(parts) == 1 {
		return grams, true
	}

	fraction := parts[1]
	if fraction == "" {
		return grams, true
	}
	if len(fraction) > 3 {
		fraction = fraction[:3]
	}
	for len(fraction) < 3 {
		fraction += "0"
	}

	extra, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, false
	}

	return grams + extra, true
}
