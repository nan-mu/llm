package frontend

import (
	"errors"
	"fmt"
)

const (
	CodeNotFound           = "MODEL_NOT_FOUND"
	CodeNotRunning         = "MODEL_NOT_RUNNING"
	CodeAlreadyRunning     = "MODEL_ALREADY_RUNNING"
	CodeLoadFailed         = "MODEL_LOAD_FAILED"
	CodeUnloadFailed       = "MODEL_UNLOAD_FAILED"
	CodeBackendUnavailable = "BACKEND_UNAVAILABLE"
)

// ErrNotImplemented is returned by Inferencer methods until OpenAI proxying is wired.
var ErrNotImplemented = errors.New("not implemented")

// ErrNotReady is returned when the Unix frontend process is not accepting HTTP.
var ErrNotReady = errors.New("frontend not ready")

// Error is a frontend protocol or process error.
type Error struct {
	Code       string
	Message    string
	StatusCode int
	Err        error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Code + ": " + e.Message
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Err }

// Is reports whether err is an Error with the given code.
func Is(err error, code string) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Code == code
	}
	return false
}

func WrapUnavailable(op string, err error) error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return err
	}
	return &Error{
		Code:    CodeBackendUnavailable,
		Message: fmt.Sprintf("%s: %v", op, err),
		Err:     err,
	}
}
