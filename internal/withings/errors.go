package withings

import "errors"

var (
	ErrAuthenticationRequired = errors.New("withings authentication required")
	ErrRateLimited            = errors.New("withings rate limited")
	ErrProtocol               = errors.New("withings protocol error")
	ErrInvalidMeasurement     = errors.New("invalid withings weight measurement")
)

// APIError describes a non-successful Withings API envelope.
type APIError struct {
	Status int
}

func (e *APIError) Error() string {
	return "withings API returned status " + itoa(e.Status)
}

func (e *APIError) Is(target error) bool {
	switch target {
	case ErrAuthenticationRequired:
		return e.Status == 343
	case ErrRateLimited:
		return e.Status == 601
	case ErrProtocol:
		return e.Status != 0 && e.Status != 100
	default:
		return false
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}

	negative := value < 0
	if negative {
		value = -value
	}

	var digits [20]byte
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte(value%10) + '0'
		value /= 10
	}
	if negative {
		pos--
		digits[pos] = '-'
	}
	return string(digits[pos:])
}
