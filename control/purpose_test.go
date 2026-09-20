package control

import "testing"

func TestGetPurposeContracts(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	tr, err := env.svc.GetPurposeTranslation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tr.TemperatureDefault != 0.7 || tr.TopKDefault != 20 {
		t.Fatalf("translation defaults: %+v", tr)
	}
	st, err := env.svc.GetPurposeStructuredTranslation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.RequireChatTemplate || st.ChatTemplateID != "translategemma" {
		t.Fatalf("structured contract: %+v", st)
	}
	if st.AllowTypeImage {
		t.Fatal("allow_type_image should be false this slice")
	}
	snap, err := env.svc.GetSnapshot(ctx, idGemma)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Purpose != "structured_translation" {
		t.Fatalf("Gemma purpose = %q", snap.Purpose)
	}
}
