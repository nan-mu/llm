package modelstate

import "testing"

func TestCanLoad(t *testing.T) {
	if err := CanLoad(FrontendReady); err != nil {
		t.Fatal(err)
	}
	if err := CanLoad(FrontendStopped); err == nil {
		t.Fatal("expected not ready")
	}
}

func TestFrontendFromPath(t *testing.T) {
	if got := FrontendFromPath("models/HY-MT2-7B-Q8_0.gguf"); got != FrontendLlama {
		t.Fatalf("gguf frontend = %s", got)
	}
	if got := FrontendFromPath("models/HY-MT2-7B-Q8_0.GGUF"); got != FrontendLlama {
		t.Fatalf("GGUF frontend = %s", got)
	}
	if got := FrontendFromPath("models/translategemma-12b-it-6bit"); got != FrontendMlxlm {
		t.Fatalf("mlx frontend = %s", got)
	}
}

func TestNativeID(t *testing.T) {
	if got := NativeID("ignored", "models/HY-MT2-7B-Q8_0.gguf"); got != "HY-MT2-7B-Q8_0" {
		t.Fatalf("native id = %s", got)
	}
	if got := NativeID("translategemma-12b-it-6bit", "models/translategemma-12b-it-6bit"); got != "translategemma-12b-it-6bit" {
		t.Fatalf("native id = %s", got)
	}
}

func TestValid(t *testing.T) {
	if !ValidFrontend(FrontendMlxlm) || ValidFrontend("dataplane") {
		t.Fatal("frontend validity")
	}
	if !ValidPurpose(PurposeStructuredTranslation) || ValidPurpose("chat") {
		t.Fatal("purpose validity")
	}
}
