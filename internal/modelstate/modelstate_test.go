package modelstate_test

import (
	"errors"
	"testing"

	"encore.app/internal/modelstate"
)

func TestCanLoad(t *testing.T) {
	t.Parallel()

	if err := modelstate.CanLoad(modelstate.FrontendReady); err != nil {
		t.Fatalf("ready: %v", err)
	}

	states := []modelstate.FrontendState{
		modelstate.FrontendStopped,
		modelstate.FrontendStarting,
		modelstate.FrontendLoading,
		modelstate.FrontendStopping,
		modelstate.FrontendFailed,
		"",
	}
	for _, st := range states {
		err := modelstate.CanLoad(st)
		if !errors.Is(err, modelstate.ErrFrontendNotReady) {
			t.Fatalf("CanLoad(%q) = %v, want ErrFrontendNotReady", st, err)
		}
	}
}

func TestFrontendFromPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path string
		want modelstate.FrontendKind
	}{
		{"models/fun-asr-nano-2512-q8_0.gguf", modelstate.FrontendLlama},
		{"models/HY-MT2-7B-Q8_0.GGUF", modelstate.FrontendLlama},
		{"models/translategemma-12b-it-6bit", modelstate.FrontendMlxlm},
		{"/abs/dir/model", modelstate.FrontendMlxlm},
	}
	for _, tc := range cases {
		if got := modelstate.FrontendFromPath(tc.path); got != tc.want {
			t.Fatalf("FrontendFromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestAllFrontends(t *testing.T) {
	t.Parallel()
	got := modelstate.AllFrontends()
	if len(got) != 3 || got[0] != modelstate.FrontendLlama || got[1] != modelstate.FrontendMlxcel || got[2] != modelstate.FrontendMlxlm {
		t.Fatalf("AllFrontends = %v", got)
	}
	if !modelstate.ValidFrontend(modelstate.FrontendLlama) || !modelstate.ValidFrontend(modelstate.FrontendMlxcel) || !modelstate.ValidFrontend(modelstate.FrontendMlxlm) {
		t.Fatal("seed frontends must be valid")
	}
	if modelstate.ValidFrontend("mlxaudio") {
		t.Fatal("unknown frontend should be invalid until registered")
	}
}

func TestNativeID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id, path, want string
	}{
		{"fun-asr-nano-2512-q8_0", "models/fun-asr-nano-2512-q8_0.gguf", "fun-asr-nano-2512-q8_0"},
		{"HY-MT2-7B-Q8_0", "models/HY-MT2-7B-Q8_0.GGUF", "HY-MT2-7B-Q8_0"},
		{"translategemma-12b-it-6bit", "models/translategemma-12b-it-6bit", "translategemma-12b-it-6bit"},
		{"logical", "models/other-name.gguf", "other-name"},
	}
	for _, tc := range cases {
		if got := modelstate.NativeID(tc.id, tc.path); got != tc.want {
			t.Fatalf("NativeID(%q, %q) = %q, want %q", tc.id, tc.path, got, tc.want)
		}
	}
}
