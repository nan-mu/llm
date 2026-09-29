// Package modelstate is the residency contract between control and gateway.
// Control writes catalog state. Gateway only reads snapshots through control APIs.
package modelstate

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrFrontendNotReady is returned when a model load is attempted before the frontend is READY.
var ErrFrontendNotReady = errors.New("frontend is not ready")

// CanLoad reports whether catalog policy allows loading a model on this frontend.
// Workers do not implement this rule. Control applies it before Runtime.Load.
func CanLoad(frontend FrontendState) error {
	if frontend != FrontendReady {
		return ErrFrontendNotReady
	}
	return nil
}

// ModelState is residency of a logical model on its frontend.
type ModelState string

const (
	ModelUnloaded  ModelState = "unloaded"
	ModelLoading   ModelState = "loading"
	ModelLoaded    ModelState = "loaded"
	ModelUnloading ModelState = "unloading"
	ModelFailed    ModelState = "failed"
)

// FrontendState is the process behind a Unix frontend.
type FrontendState string

const (
	FrontendStopped  FrontendState = "stopped"
	FrontendStarting FrontendState = "starting"
	FrontendLoading  FrontendState = "loading"
	FrontendReady    FrontendState = "ready"
	FrontendStopping FrontendState = "stopping"
	FrontendFailed   FrontendState = "failed"
)

// FrontendKind names a Unix frontend.
type FrontendKind string

const (
	FrontendLlama  FrontendKind = "llama"
	FrontendMlxcel FrontendKind = "mlxcel"
	FrontendMlxlm  FrontendKind = "mlxlm"
)

// AllFrontends is the catalog order of Unix frontends.
func AllFrontends() []FrontendKind {
	return []FrontendKind{FrontendLlama, FrontendMlxcel, FrontendMlxlm}
}

// Purpose is which OpenAI route a model may serve.
type Purpose string

const (
	PurposeASR                   Purpose = "asr"
	PurposeTranslation           Purpose = "translation"
	PurposeStructuredTranslation Purpose = "structured_translation"
)

const ggufExt = ".gguf"

// FrontendFromPath infers the Unix frontend from a weights path.
// Non-GGUF defaults to mlxlm. mlxcel is only selected by an explicit catalog row.
func FrontendFromPath(path string) FrontendKind {
	if strings.HasSuffix(strings.ToLower(path), ggufExt) {
		return FrontendLlama
	}
	return FrontendMlxlm
}

// NativeID is the identifier passed to a frontend for one catalog row.
// GGUF paths use the basename without .gguf. Other paths use the catalog id.
func NativeID(id, path string) string {
	base := filepath.Base(path)
	if strings.HasSuffix(strings.ToLower(base), ggufExt) {
		return base[:len(base)-len(ggufExt)]
	}
	return id
}

// ValidFrontend reports whether k is a known Unix frontend.
func ValidFrontend(k FrontendKind) bool {
	for _, f := range AllFrontends() {
		if f == k {
			return true
		}
	}
	return false
}

// ValidPurpose reports whether p is a known model purpose.
func ValidPurpose(p Purpose) bool {
	switch p {
	case PurposeASR, PurposeTranslation, PurposeStructuredTranslation:
		return true
	default:
		return false
	}
}
