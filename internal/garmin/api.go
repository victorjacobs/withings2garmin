package garmin

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

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

func (client *Client) Validate(ctx context.Context, accessToken string) error {
	_, err := client.doAPI(ctx, http.MethodGet, "/userprofile-service/socialProfile", nil, accessToken)

	return err
}

func (client *Client) DayView(
	ctx context.Context,
	accessToken string,
	localDate time.Time,
) ([]WeightSample, error) {
	endpoint := "/weight-service/weight/dayview/" + localDate.Format("2006-01-02") + "?includeAll=true"
	body, err := client.doAPI(ctx, http.MethodGet, endpoint, nil, accessToken)
	if err != nil {
		return nil, err
	}

	samples, err := decodeWeightSamples(body)
	if err != nil {
		return nil, fmt.Errorf("decode Garmin day view: %w", err)
	}

	return samples, nil
}

func (client *Client) DateRange(
	ctx context.Context,
	accessToken string,
	start, end time.Time,
) ([]WeightSample, error) {
	endpoint := "/weight-service/weight/dateRange?startDate=" + start.Format("2006-01-02") +
		"&endDate=" + end.Format("2006-01-02")
	body, err := client.doAPI(ctx, http.MethodGet, endpoint, nil, accessToken)
	if err != nil {
		return nil, err
	}

	samples, err := decodeWeightSamples(body)
	if err != nil {
		return nil, fmt.Errorf("decode Garmin date range: %w", err)
	}

	return samples, nil
}

func (client *Client) UploadWeight(
	ctx context.Context,
	accessToken string,
	measuredAt time.Time,
	location *time.Location,
	grams int64,
) error {
	body, err := marshalWeightUpload(measuredAt, location, grams)
	if err != nil {
		return err
	}

	_, err = client.doAPI(ctx, http.MethodPost, "/weight-service/user-weight", body, accessToken)

	return err
}

func (client *Client) doAPI(
	ctx context.Context,
	method, endpoint string,
	body []byte,
	accessToken string,
) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, client.apiURL(endpoint), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create Garmin API request: %w", err)
	}
	setNativeHeaders(request.Header)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := client.httpClient.Do(request)
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

func (client *Client) apiURL(endpoint string) string {
	return resolveEndpoint(client.apiBase, endpoint)
}

func (client *Client) diURL(endpoint string) string {
	return resolveEndpoint(client.diBase, endpoint)
}

func resolveEndpoint(base *url.URL, endpoint string) string {
	resolved := *base
	parts := strings.SplitN(endpoint, "?", 2)
	resolved.Path = path.Join(resolved.Path, parts[0])
	if len(parts) == 2 {
		resolved.RawQuery = parts[1]
	}

	return resolved.String()
}

func setNativeHeaders(headers http.Header) {
	headers.Set("User-Agent", "GCM-Android-5.23")
	headers.Set(
		"X-Garmin-User-Agent",
		"com.garmin.android.apps.connectmobile/5.23; ; Google/sdk_gphone64_arm64/google; Android/33; Dalvik/2.1.0",
	)
	headers.Set("X-Garmin-Paired-App-Version", "10861")
	headers.Set("X-Garmin-Client-Platform", "Android")
	headers.Set("X-App-Ver", "10861")
	headers.Set("X-Lang", "en")
	headers.Set("X-GCExperience", "GC5")
	headers.Set("Accept-Language", "en-US,en;q=0.9")
}
