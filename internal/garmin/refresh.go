package garmin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (client *Client) Refresh(ctx context.Context, tokens TokenSet) (TokenSet, error) {
	if tokens.ClientID == "" || tokens.RefreshToken == "" {
		return TokenSet{}, ErrAuthenticationRequired
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {tokens.ClientID},
		"refresh_token": {tokens.RefreshToken},
	}
	endpoint := client.diURL("/di-oauth2-service/oauth/token")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenSet{}, fmt.Errorf("create Garmin refresh request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Authorization", basicAuthorization(tokens.ClientID))
	setNativeHeaders(request.Header)

	response, err := client.httpClient.Do(request)
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

func basicAuthorization(clientID string) string {
	credentials := base64.StdEncoding.EncodeToString([]byte(clientID + ":"))

	return "Basic " + credentials
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
}
