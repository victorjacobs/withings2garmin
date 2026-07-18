package garmin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 4 << 20

type Client struct {
	httpClient *http.Client
	apiBase    *url.URL
	diBase     *url.URL
}

func NewClient(httpClient *http.Client, apiBase, diBase string) (*Client, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	if apiBase == "" {
		apiBase = defaultAPIBase
	}
	if diBase == "" {
		diBase = defaultDIBase
	}

	parsedAPIBase, err := url.Parse(apiBase)
	if err != nil {
		return nil, fmt.Errorf("parse Garmin API base: %w", err)
	}
	parsedDIBase, err := url.Parse(diBase)
	if err != nil {
		return nil, fmt.Errorf("parse Garmin DI base: %w", err)
	}

	return &Client{httpClient: httpClient, apiBase: parsedAPIBase, diBase: parsedDIBase}, nil
}

func (c *Client) Validate(ctx context.Context, accessToken string) error {
	_, err := c.doAPI(ctx, http.MethodGet, "/userprofile-service/socialProfile", nil, accessToken)
	return err
}

func (c *Client) DayView(ctx context.Context, accessToken string, localDate time.Time) ([]WeightSample, error) {
	endpoint := "/weight-service/weight/dayview/" + localDate.Format("2006-01-02") + "?includeAll=true"
	body, err := c.doAPI(ctx, http.MethodGet, endpoint, nil, accessToken)
	if err != nil {
		return nil, err
	}

	samples, err := decodeWeightSamples(body)
	if err != nil {
		return nil, fmt.Errorf("decode Garmin day view: %w", err)
	}
	return samples, nil
}

func (c *Client) DateRange(ctx context.Context, accessToken string, start, end time.Time) ([]WeightSample, error) {
	endpoint := "/weight-service/weight/dateRange?startDate=" + start.Format("2006-01-02") + "&endDate=" + end.Format("2006-01-02")
	body, err := c.doAPI(ctx, http.MethodGet, endpoint, nil, accessToken)
	if err != nil {
		return nil, err
	}

	samples, err := decodeWeightSamples(body)
	if err != nil {
		return nil, fmt.Errorf("decode Garmin date range: %w", err)
	}
	return samples, nil
}

func (c *Client) UploadWeight(ctx context.Context, accessToken string, measuredAt time.Time, location *time.Location, grams int64) error {
	body, err := marshalWeightUpload(measuredAt, location, grams)
	if err != nil {
		return err
	}
	_, err = c.doAPI(ctx, http.MethodPost, "/weight-service/user-weight", body, accessToken)
	return err
}

func (c *Client) Refresh(ctx context.Context, tokens TokenSet) (TokenSet, error) {
	if tokens.ClientID == "" || tokens.RefreshToken == "" {
		return TokenSet{}, ErrAuthenticationRequired
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {tokens.ClientID},
		"refresh_token": {tokens.RefreshToken},
	}
	endpoint := c.diURL("/di-oauth2-service/oauth/token")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenSet{}, fmt.Errorf("create Garmin refresh request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Authorization", basicAuthorization(tokens.ClientID))
	setNativeHeaders(request.Header)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return TokenSet{}, fmt.Errorf("refresh Garmin token: %w", err)
	}
	defer func() {
		// A response was fully consumed; closing cannot affect the API result.
		_ = response.Body.Close()
	}()
	responseBody, err := readBounded(response.Body)
	if err != nil {
		return TokenSet{}, err
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return TokenSet{}, ErrAuthenticationRequired
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return TokenSet{}, ErrRateLimited
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return TokenSet{}, fmt.Errorf("refresh Garmin token: HTTP %d: %w", response.StatusCode, ErrProtocol)
	}

	var tokenResponse tokenResponse
	if err := json.Unmarshal(responseBody, &tokenResponse); err != nil {
		return TokenSet{}, fmt.Errorf("decode Garmin refresh: %w", err)
	}
	refreshed, err := tokenSetFromResponse(tokenResponse, tokens.RefreshToken)
	if err != nil {
		return TokenSet{}, err
	}
	return refreshed, nil
}

func (c *Client) doAPI(ctx context.Context, method, endpoint string, body []byte, accessToken string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.apiURL(endpoint), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create Garmin API request: %w", err)
	}
	setNativeHeaders(request.Header)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("garmin API request: %w", err)
	}
	defer func() {
		// A response was fully consumed; closing cannot affect the API result.
		_ = response.Body.Close()
	}()
	responseBody, err := readBounded(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, ErrAuthenticationRequired
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, ErrRateLimited
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("garmin API HTTP %d: %w", response.StatusCode, ErrProtocol)
	}
	return responseBody, nil
}

func (c *Client) apiURL(endpoint string) string { return resolveEndpoint(c.apiBase, endpoint) }
func (c *Client) diURL(endpoint string) string  { return resolveEndpoint(c.diBase, endpoint) }
func resolveEndpoint(base *url.URL, endpoint string) string {
	u := *base
	parts := strings.SplitN(endpoint, "?", 2)
	u.Path = path.Join(u.Path, parts[0])
	if len(parts) == 2 {
		u.RawQuery = parts[1]
	}
	return u.String()
}

func setNativeHeaders(headers http.Header) {
	headers.Set("User-Agent", "GCM-Android-5.23")
	headers.Set("X-Garmin-User-Agent", "com.garmin.android.apps.connectmobile/5.23; ; Google/sdk_gphone64_arm64/google; Android/33; Dalvik/2.1.0")
	headers.Set("X-Garmin-Paired-App-Version", "10861")
	headers.Set("X-Garmin-Client-Platform", "Android")
	headers.Set("X-App-Ver", "10861")
	headers.Set("X-Lang", "en")
	headers.Set("X-GCExperience", "GC5")
	headers.Set("Accept-Language", "en-US,en;q=0.9")
}

func basicAuthorization(clientID string) string { return "Basic " + base64Encode(clientID+":") }
func base64Encode(value string) string          { return base64.StdEncoding.EncodeToString([]byte(value)) }

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

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
}

func decodeWeightSamples(body []byte) ([]WeightSample, error) {
	var document any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	return findSamples(document)
}

func findSamples(value any) ([]WeightSample, error) {
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
	return found, nil
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
