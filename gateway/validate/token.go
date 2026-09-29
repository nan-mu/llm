package validate

import "strings"

// APIToken is a no-op auth check for this slice. A bearer token may be present and is ignored.
func APIToken(authorization string) *Error {
	_ = strings.TrimSpace(authorization)
	return nil
}
