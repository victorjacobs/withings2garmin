package withings

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAuthorizeURL = "https://account.withings.com/oauth2_user/authorize2"
	defaultTokenURL     = "https://wbsapi.withings.net/v2/oauth2"
)

type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	AuthorizeURL string
	TokenURL     string
}

func (config OAuthConfig) authorizeURL() string {
	if config.AuthorizeURL == "" {
		return defaultAuthorizeURL
	}
	return config.AuthorizeURL
}

func (config OAuthConfig) tokenURL() string {
	if config.TokenURL == "" {
		return defaultTokenURL
	}
	return config.TokenURL
}

func (config OAuthConfig) validate() error {
	if config.ClientID == "" || config.ClientSecret == "" || config.RedirectURI == "" {
		return fmt.Errorf("%w: missing OAuth client ID, secret, or redirect URI", ErrProtocol)
	}
	if _, err := url.ParseRequestURI(config.RedirectURI); err != nil {
		return fmt.Errorf("%w: invalid redirect URI: %v", ErrProtocol, err)
	}
	return nil
}

func NewState() (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate OAuth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(random[:]), nil
}

func (config OAuthConfig) AuthorizationURL(state string) (string, error) {
	if err := config.validate(); err != nil {
		return "", err
	}
	if state == "" {
		return "", fmt.Errorf("%w: OAuth state is empty", ErrProtocol)
	}

	authorizeURL, err := url.Parse(config.authorizeURL())
	if err != nil {
		return "", fmt.Errorf("%w: invalid authorization endpoint: %v", ErrProtocol, err)
	}
	values := authorizeURL.Query()
	values.Set("response_type", "code")
	values.Set("client_id", config.ClientID)
	values.Set("redirect_uri", config.RedirectURI)
	values.Set("scope", "user.metrics")
	values.Set("state", state)
	authorizeURL.RawQuery = values.Encode()
	return authorizeURL.String(), nil
}

// ParseRedirect validates a browser callback. It intentionally does not accept a
// raw authorization code: doing so would bypass state validation.
func ParseRedirect(redirectURL, expectedState string) (string, error) {
	callback, err := url.Parse(redirectURL)
	if err != nil {
		return "", fmt.Errorf("%w: parse OAuth redirect: %v", ErrProtocol, err)
	}
	values := callback.Query()
	if returnedError := values.Get("error"); returnedError != "" {
		return "", fmt.Errorf("OAuth authorization failed: %s", returnedError)
	}
	if expectedState == "" || values.Get("state") != expectedState {
		return "", fmt.Errorf("%w: OAuth state does not match", ErrProtocol)
	}
	code := values.Get("code")
	if code == "" {
		return "", fmt.Errorf("%w: OAuth redirect has no code", ErrProtocol)
	}
	return code, nil
}

func (client *Client) ExchangeCode(ctx context.Context, config OAuthConfig, code string) (Token, error) {
	if err := config.validate(); err != nil {
		return Token{}, err
	}
	if strings.TrimSpace(code) == "" {
		return Token{}, fmt.Errorf("%w: authorization code is empty", ErrProtocol)
	}
	return client.requestToken(ctx, config, url.Values{
		"action":        {"requesttoken"},
		"grant_type":    {"authorization_code"},
		"client_id":     {config.ClientID},
		"client_secret": {config.ClientSecret},
		"code":          {code},
		"redirect_uri":  {config.RedirectURI},
	})
}

func (client *Client) RefreshToken(ctx context.Context, config OAuthConfig, refreshToken string) (Token, error) {
	if err := config.validate(); err != nil {
		return Token{}, err
	}
	if refreshToken == "" {
		return Token{}, fmt.Errorf("%w: refresh token is empty", ErrAuthenticationRequired)
	}
	return client.requestToken(ctx, config, url.Values{
		"action":        {"requesttoken"},
		"grant_type":    {"refresh_token"},
		"client_id":     {config.ClientID},
		"client_secret": {config.ClientSecret},
		"refresh_token": {refreshToken},
	})
}

type tokenEnvelope struct {
	Status int `json:"status"`
	Body   struct {
		UserID       json.RawMessage `json:"userid"`
		AccessToken  string          `json:"access_token"`
		RefreshToken string          `json:"refresh_token"`
		Scope        string          `json:"scope"`
		TokenType    string          `json:"token_type"`
		ExpiresIn    int64           `json:"expires_in"`
	} `json:"body"`
}

func (client *Client) requestToken(ctx context.Context, config OAuthConfig, values url.Values) (Token, error) {
	envelope := tokenEnvelope{}
	if err := client.postForm(ctx, config.tokenURL(), "", values, client.tokenBodyLimit, &envelope); err != nil {
		return Token{}, fmt.Errorf("request Withings token: %w", err)
	}
	if envelope.Status != 0 {
		return Token{}, &APIError{Status: envelope.Status}
	}

	userID, err := rawString(envelope.Body.UserID)
	if err != nil {
		return Token{}, fmt.Errorf("%w: invalid token user ID: %v", ErrProtocol, err)
	}
	if userID == "" ||
		envelope.Body.AccessToken == "" ||
		envelope.Body.RefreshToken == "" ||
		envelope.Body.ExpiresIn <= 0 {
		return Token{}, fmt.Errorf("%w: token response omitted required fields", ErrProtocol)
	}

	now := client.now().UTC()
	return Token{
		SchemaVersion: 1,
		UserID:        userID,
		AccessToken:   envelope.Body.AccessToken,
		RefreshToken:  envelope.Body.RefreshToken,
		Scope:         envelope.Body.Scope,
		TokenType:     envelope.Body.TokenType,
		ObtainedAt:    now,
		ExpiresAt:     now.Add(time.Duration(envelope.Body.ExpiresIn) * time.Second),
	}, nil
}
