package garmin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type TokenSet struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ClientID     string    `json:"client_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (t TokenSet) NeedsRefresh(now time.Time) bool {
	return t.AccessToken == "" || t.ExpiresAt.IsZero() || !now.Before(t.ExpiresAt.Add(-15*time.Minute))
}

func tokenSetFromResponse(response tokenResponse, fallbackRefresh string) (TokenSet, error) {
	if response.AccessToken == "" {
		return TokenSet{}, fmt.Errorf("DI token response: %w: missing access token", ErrProtocol)
	}

	tokens := TokenSet{AccessToken: response.AccessToken, RefreshToken: response.RefreshToken, ClientID: response.ClientID}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = fallbackRefresh
	}

	claims, err := jwtClaims(response.AccessToken)
	if err == nil {
		if tokens.ClientID == "" {
			tokens.ClientID = claims.ClientID
		}
		if claims.Exp > 0 {
			tokens.ExpiresAt = time.Unix(claims.Exp, 0).UTC()
		}
	}
	if tokens.ClientID == "" {
		return TokenSet{}, fmt.Errorf("DI token response: %w: missing client ID", ErrProtocol)
	}
	if tokens.ExpiresAt.IsZero() {
		return TokenSet{}, fmt.Errorf("DI token response: %w: JWT expiry unavailable", ErrProtocol)
	}

	return tokens, nil
}

type jwtTokenClaims struct {
	ClientID string `json:"client_id"`
	Exp      int64  `json:"exp"`
}

func jwtClaims(token string) (jwtTokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwtTokenClaims{}, fmt.Errorf("invalid JWT shape")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtTokenClaims{}, fmt.Errorf("decode JWT payload: %w", err)
	}

	var claims jwtTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return jwtTokenClaims{}, fmt.Errorf("decode JWT claims: %w", err)
	}

	return claims, nil
}
