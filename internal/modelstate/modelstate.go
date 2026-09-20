package modelstate

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrFrontendNotReady is returned when a model Load is attempted before the Unix frontend is READY.
var ErrFrontendNotReady = errors.New("frontend is not ready")

// CanLoad reports whether catalog policy allows loading a model on this frontend.
// unix must not call this; control applies it before Runtime.Load.
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

// FrontendState is the llama / mlxcel / mlxlm process behind a Unix socket.
type FrontendState string

const (
	FrontendStopped  FrontendState = "stopped"
	FrontendStarting FrontendState = "starting"
	FrontendLoading  FrontendState = "loading" // process up; model weights still loading
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

// AllFrontends is the catalog order of Unix frontends. Register a new backend
// here, in the frontends table, and in control's runtime map.
func AllFrontends() []FrontendKind {
	return []FrontendKind{FrontendLlama, FrontendMlxcel, FrontendMlxlm}
}

// SupervisorFrontend reports whether kind uses one OS process per loaded model
// (Start is a ready flag only; Load spawns the worker).
func SupervisorFrontend(k FrontendKind) bool {
	return k == FrontendLlama || k == FrontendMlxlm
}

// Purpose is which OpenAI route a model may serve.
type Purpose string

const (
	PurposeASR                   Purpose = "asr"
	PurposeTranslation           Purpose = "translation"
	PurposeStructuredTranslation Purpose = "structured_translation"
)

// ModelSnapshot is the read-only view gateway may use.
type ModelSnapshot struct {
	ID         string
	Frontend   FrontendKind
	Path       string
	Purpose    Purpose
	NativeID   string
	Desired    ModelState
	Observed   ModelState
	SocketPath string
	LastError  string
	PID        *int64
	MemoryMB   *int64
}

const ggufExt = ".gguf"

// FrontendFromPath infers the Unix frontend from a weights path.
// Purpose must not be used: ASR and translation can run on either frontend.
// Non-GGUF defaults to mlxlm; mlxcel is only via an explicit catalog row.
func FrontendFromPath(path string) FrontendKind {
	if strings.HasSuffix(strings.ToLower(path), ggufExt) {
		return FrontendLlama
	}
	return FrontendMlxlm
}

// NativeID is the identifier passed to unix.Load/Unload.
// GGUF: basename without a trailing .gguf (any case). MLX: the logical id.
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

// ValidPurpose reports whether p is a known catalog purpose.
func ValidPurpose(p Purpose) bool {
	return p == PurposeASR || p == PurposeTranslation || p == PurposeStructuredTranslation
}

// ChatPurpose reports whether p may be served by POST /v1/chat/completions.
func ChatPurpose(p Purpose) bool {
	return p == PurposeTranslation || p == PurposeStructuredTranslation
}

// FrontendSnapshot is the read-only view of a Unix frontend.
type FrontendSnapshot struct {
	Kind       FrontendKind
	SocketPath string
	Observed   FrontendState
	LastError  string
	PIDs       []int64
	MemoryMB   *int64
}

// Reader is the only modelstate API gateway should depend on.
// control implements the writer side in the control package, not here.
type Reader interface {
	Get(id string) (ModelSnapshot, bool)
	All() []ModelSnapshot
}
