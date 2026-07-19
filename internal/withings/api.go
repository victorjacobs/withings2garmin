package withings

import (
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
		if err := client.postForm(
			ctx,
			client.measureURL,
			accessToken,
			values,
			client.measureBodyLimit,
			&envelope,
		); err != nil {
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

func (client *Client) postForm(
	ctx context.Context,
	endpoint, bearer string,
	values url.Values,
	limit int64,
	destination any,
) error {
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
