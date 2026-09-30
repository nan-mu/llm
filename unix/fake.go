package unix

import (
	"encore.app/unix/internal/fakemlx"
	"encore.app/unix/internal/fakeux"
)

// BuildFakeFrontend compiles the test-only HTTP-over-UDS frontend (fakeux).
func BuildFakeFrontend() (string, error) {
	return fakeux.Build()
}

// BuildFakeMlxlm compiles the health-only mlxlm child used in tests (fakemlx).
func BuildFakeMlxlm() (string, error) {
	return fakemlx.Build()
}
