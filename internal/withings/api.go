package withings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMeasureURL   = "https://wbsapi.withings.net/measure"
	defaultTokenLimit   = int64(4 << 20)
	defaultMeasureLimit = int64(16 << 20)
	maximumPages        = 10000
)

// Client talks to the official Withings Public API. Its endpoints are injectable
// solely for tests and for isolating the published API contract.
type Client struct {
	httpClient       *http.Client
	measureURL       string
	tokenBodyLimit   int64
	measureBodyLimit int64
	now              func() time.Time
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		httpClient:       httpClient,
		measureURL:       defaultMeasureURL,
		tokenBodyLimit:   defaultTokenLimit,
		measureBodyLimit: defaultMeasureLimit,
		now:              time.Now,
	}
}

// SetMeasureURL is intended for offline contract tests.
func (client *Client) SetMeasureURL(endpoint string) {
	client.measureURL = endpoint
}

func (client *Client) FetchMeasurements(ctx context.Context, accessToken string, query Query) (FetchResult, error) {
	if accessToken == "" {
		return FetchResult{}, fmt.Errorf("%w: missing access token", ErrAuthenticationRequired)
	}
	if err := query.validate(); err != nil {
		return FetchResult{}, err
	}

	values := url.Values{
		"action":   {"getmeas"},
		"meastype": {"1"},
		"category": {"1"},
	}
	if query.LastUpdate != nil {
		values.Set("lastupdate", strconv.FormatInt(query.LastUpdate.UTC().Unix(), 10))
	} else if query.StartDate != nil {
		values.Set("startdate", strconv.FormatInt(query.StartDate.UTC().Unix(), 10))
		values.Set("enddate", strconv.FormatInt(query.EndDate.UTC().Unix(), 10))
	}

	result := FetchResult{}
	seenOffsets := make(map[string]struct{})
	var candidateCursor int64
	for page := 0; page < maximumPages; page++ {
		envelope := measureEnvelope{}
		if err := client.postForm(ctx, client.measureURL, accessToken, values, client.measureBodyLimit, &envelope); err != nil {
			return FetchResult{}, fmt.Errorf("fetch Withings measurements: %w", err)
		}
		result.PageCount++
		if envelope.Status == 100 {
			result.NoData = true
			if envelope.Body.UpdateTime > 0 {
				result.UpdateTime = time.Unix(envelope.Body.UpdateTime, 0).UTC()
			}
			return result, nil
		}
		if envelope.Status != 0 {
			return FetchResult{}, &APIError{Status: envelope.Status}
		}

		groups, err := envelope.Body.groups()
		if err != nil {
			return FetchResult{}, err
		}
		result.Groups = append(result.Groups, groups...)
		if envelope.Body.UpdateTime > 0 && (candidateCursor == 0 || envelope.Body.UpdateTime < candidateCursor) {
			candidateCursor = envelope.Body.UpdateTime
		}
		if envelope.Body.More == 0 {
			if candidateCursor > 0 {
				result.UpdateTime = time.Unix(candidateCursor, 0).UTC()
			}
			return result, nil
		}
		offset := string(envelope.Body.Offset)
		if envelope.Body.More != 1 || offset == "" {
			return FetchResult{}, fmt.Errorf("%w: invalid measurement pagination response", ErrProtocol)
		}
		if _, exists := seenOffsets[offset]; exists {
			return FetchResult{}, fmt.Errorf("%w: repeated measurement offset", ErrProtocol)
		}
		seenOffsets[offset] = struct{}{}
		values.Set("offset", offset)
	}
	return FetchResult{}, fmt.Errorf("%w: measurement page limit exceeded", ErrProtocol)
}

func (client *Client) postForm(ctx context.Context, endpoint, bearer string, values url.Values, limit int64, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer func() {
		// A response was fully consumed; closing cannot affect the API result.
		_ = response.Body.Close()
	}()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode == http.StatusTooManyRequests {
			return ErrRateLimited
		}
		return fmt.Errorf("%w: HTTP status %d", ErrProtocol, response.StatusCode)
	}
	if err := decodeJSON(response.Body, limit, destination); err != nil {
		return err
	}
	return nil
}

func decodeJSON(reader io.Reader, limit int64, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, limit+1))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrProtocol, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON", ErrProtocol)
		}
		return fmt.Errorf("%w: trailing JSON: %v", ErrProtocol, err)
	}
	return nil
}

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
