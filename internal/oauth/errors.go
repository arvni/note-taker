package oauth

import "fmt"

// TokenError is returned when Zoho's token endpoint replies with an error field
// (e.g. invalid_code / invalid_client). It signals an authentication failure —
// as opposed to a transport error — so callers can distinguish a permanently
// invalid refresh token (revoke, spec §18) from a transient outage (retry).
type TokenError struct {
	Code string
}

func (e *TokenError) Error() string { return "zoho token error: " + e.Code }

// IsInvalidGrant reports whether the error indicates the refresh token itself is
// no longer valid and must not be retried (spec §18).
func (e *TokenError) IsInvalidGrant() bool {
	switch e.Code {
	case "invalid_code", "invalid_grant", "invalid_token", "invalid_client", "unauthorized":
		return true
	default:
		return false
	}
}

var _ error = (*TokenError)(nil)

// wrapTokenError is used internally to annotate context.
func wrapTokenError(code string) error { return fmt.Errorf("%w", &TokenError{Code: code}) }
