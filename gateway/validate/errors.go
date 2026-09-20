package validate

// Error is an OpenAI-shaped error for chat completions.
type Error struct {
	Message string
	Type    string
	Code    string
}

func invalid(msg, code string) *Error {
	return &Error{Message: msg, Type: "invalid_request_error", Code: code}
}

func server(msg string) *Error {
	return &Error{Message: msg, Type: "server_error"}
}
