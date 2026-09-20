package validate

import "strings"

// APIToken is a no-op auth check for this slice. Bearer may be present and ignored.
// Future: validate sk-local-... against gateway api_tokens.
func APIToken(authorization string) *Error {
	_ = strings.TrimSpace(authorization)
	return nil
}
