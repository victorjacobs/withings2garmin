package state

import "time"

const SchemaVersion = 1

type WithingsTokenStore interface {
	LoadWithingsTokens() (WithingsTokens, error)
	SaveWithingsTokens(WithingsTokens) error
}

type GarminTokenStore interface {
	LoadGarminTokens() (GarminTokens, error)
	SaveGarminTokens(GarminTokens) error
}

var _ WithingsTokenStore = &Store{}
var _ GarminTokenStore = &Store{}

type WithingsTokens struct {
	SchemaVersion int       `json:"schema_version"`
	UserID        string    `json:"user_id"`
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token"`
	Scope         string    `json:"scope"`
	TokenType     string    `json:"token_type"`
	ObtainedAt    time.Time `json:"obtained_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type GarminTokens struct {
	SchemaVersion int       `json:"schema_version"`
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token"`
	ClientID      string    `json:"client_id"`
	ExpiresAt     time.Time `json:"expires_at"`
	ObtainedAt    time.Time `json:"obtained_at"`
}

func (tokens WithingsTokens) validate() error {
	return validateSchemaVersion(tokens.SchemaVersion)
}

func (tokens GarminTokens) validate() error {
	return validateSchemaVersion(tokens.SchemaVersion)
}
