package garmin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

type Credentials struct{ Email, Password, MFACode string }

type Authenticator struct {
	httpClient *http.Client
	ssoBase    *url.URL
	client     *Client
}

func NewAuthenticator(httpClient *http.Client, ssoBase, apiBase, diBase string) (*Authenticator, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	if ssoBase == "" {
		ssoBase = defaultSSOBase
	}
	parsedSSOBase, err := url.Parse(ssoBase)
	if err != nil {
		return nil, fmt.Errorf("parse Garmin SSO base: %w", err)
	}
	client, err := NewClient(httpClient, apiBase, diBase)
	if err != nil {
		return nil, err
	}
	return &Authenticator{httpClient: httpClient, ssoBase: parsedSSOBase, client: client}, nil
}

// Login deliberately makes one mobile credential submission. Interactive login is
// isolated here; scheduled syncs must only use Client.Refresh.
func (a *Authenticator) Login(ctx context.Context, credentials Credentials) (TokenSet, error) {
	if credentials.Email == "" || credentials.Password == "" {
		return TokenSet{}, fmt.Errorf("garmin credentials are required")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return TokenSet{}, fmt.Errorf("create Garmin cookie jar: %w", err)
	}
	httpClient := *a.httpClient
	httpClient.Jar = jar
	ticket, serviceURL, err := a.mobileTicket(ctx, &httpClient, credentials)
	if err != nil {
		return TokenSet{}, err
	}
	tokens, err := a.exchangeTicket(ctx, ticket, serviceURL)
	if err != nil {
		return TokenSet{}, err
	}
	if err := a.client.Validate(ctx, tokens.AccessToken); err != nil {
		return TokenSet{}, fmt.Errorf("validate Garmin login: %w", err)
	}
	return tokens, nil
}

func (a *Authenticator) mobileTicket(ctx context.Context, client *http.Client, credentials Credentials) (string, string, error) {
	parameters := url.Values{
		"clientId": {iosSSOClientID},
		"locale":   {"en-US"},
		"service":  {iosServiceURL},
	}
	endpoint := resolveEndpoint(a.ssoBase, "/mobile/api/login") + "?" + parameters.Encode()
	payload, err := json.Marshal(map[string]any{
		"username":     credentials.Email,
		"password":     credentials.Password,
		"rememberMe":   true,
		"captchaToken": "",
	})
	if err != nil {
		return "", "", fmt.Errorf("encode Garmin login: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return "", "", fmt.Errorf("create Garmin login request: %w", err)
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", a.ssoBase.String())
	request.Header.Set("User-Agent", mobileSSOUserAgent)

	response, err := client.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("garmin mobile login: %w", err)
	}
	defer func() {
		// A response was fully consumed; closing cannot affect the API result.
		_ = response.Body.Close()
	}()
	body, err := readBounded(response.Body)
	if err != nil {
		return "", "", err
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return "", "", ErrRateLimited
	}
	if response.StatusCode == http.StatusForbidden {
		return "", "", fmt.Errorf("mobile login challenge: %w", ErrProtocol)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", fmt.Errorf("mobile login HTTP %d: %w", response.StatusCode, ErrProtocol)
	}

	var result loginResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", fmt.Errorf("decode Garmin mobile login: %w", err)
	}
	switch result.ResponseStatus.Type {
	case "SUCCESSFUL":
		if result.ResponseStatus.ServiceTicketID == "" {
			return "", "", fmt.Errorf("mobile login: %w: missing service ticket", ErrProtocol)
		}
		return result.ResponseStatus.ServiceTicketID, iosServiceURL, nil
	case "INVALID_USERNAME_PASSWORD":
		return "", "", ErrInvalidCredentials
	case "MFA_REQUIRED":
		return "", "", ErrMFARequired
	case "CAPTCHA_REQUIRED":
		return "", "", fmt.Errorf("mobile login captcha challenge: %w", ErrProtocol)
	default:
		return "", "", fmt.Errorf("mobile login response %q: %w", result.ResponseStatus.Type, ErrProtocol)
	}
}

func (a *Authenticator) exchangeTicket(ctx context.Context, ticket, serviceURL string) (TokenSet, error) {
	for _, clientID := range diClientIDs {
		form := url.Values{
			"client_id":      {clientID},
			"service_ticket": {ticket},
			"grant_type":     {diServiceTicketGrant},
			"service_url":    {serviceURL},
		}
		endpoint := a.client.diURL("/di-oauth2-service/oauth/token")
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return TokenSet{}, fmt.Errorf("create DI exchange request: %w", err)
		}
		setNativeHeaders(request.Header)
		request.Header.Set("Authorization", basicAuthorization(clientID))
		request.Header.Set("Accept", "application/json,text/html;q=0.9,*/*;q=0.8")
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Cache-Control", "no-cache")

		response, err := a.httpClient.Do(request)
		if err != nil {
			return TokenSet{}, fmt.Errorf("exchange Garmin service ticket: %w", err)
		}
		body, readErr := readBounded(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			return TokenSet{}, readErr
		}
		if closeErr != nil {
			return TokenSet{}, fmt.Errorf("close DI exchange response: %w", closeErr)
		}
		if response.StatusCode == http.StatusTooManyRequests {
			return TokenSet{}, ErrRateLimited
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			continue
		}
		var result tokenResponse
		if err := json.Unmarshal(body, &result); err != nil {
			continue
		}
		tokens, err := tokenSetFromResponse(result, "")
		if err == nil {
			return tokens, nil
		}
	}
	return TokenSet{}, fmt.Errorf("exchange Garmin service ticket: %w", ErrProtocol)
}

type loginResponse struct {
	ResponseStatus struct {
		Type            string `json:"type"`
		ServiceTicketID string `json:"serviceTicketId"`
	} `json:"responseStatus"`
}
