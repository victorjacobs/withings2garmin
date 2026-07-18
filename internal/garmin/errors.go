package garmin

import "errors"

var (
	ErrAuthenticationRequired = errors.New("garmin authentication required; run 'withings2garmin auth garmin' interactively")
	ErrInvalidCredentials     = errors.New("invalid Garmin credentials")
	ErrMFARequired            = errors.New("garmin multi-factor authentication required")
	ErrRateLimited            = errors.New("garmin rate limited")
	ErrProtocol               = errors.New("garmin API protocol error")
)
