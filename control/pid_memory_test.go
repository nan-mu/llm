package control

import (
	"context"
	"fmt"
	"testing"
	"time"

	"encore.app/internal/modelstate"
)

func TestTheoreticalMemoryOnLoadedGemma(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx)
	if _, err := env.svc.loadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}
	snap, err := env.svc.lookupSnapshot(ctx, idGemma)
	if err != nil {
		t.Fatal(err)
	}
	if snap.PID == nil || *snap.PID <= 0 {
		t.Fatalf("expected pid, got %v", snap.PID)
	}
	if snap.SocketPath != nil {
		t.Fatalf("mlxlm socket_path should be nil, got %q", *snap.SocketPath)
	}
	if snap.MemoryMB == nil || *snap.MemoryMB != 9560 {
		t.Fatalf("memory_mb = %v, want 9560", snap.MemoryMB)
	}
	fe, err := env.svc.lookupFrontend(ctx, "mlxlm")
	if err != nil {
		t.Fatal(err)
	}
	if fe.SocketPath != nil {
		t.Fatalf("frontend socket_path should be nil")
	}
	if len(fe.PIDs) != 1 || fe.PIDs[0] != *snap.PID {
		t.Fatalf("frontend pids = %v, model pid = %v", fe.PIDs, snap.PID)
	}
}

func TestUnloadClearsModelPID(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx)
	if _, err := env.svc.loadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.unloadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}
	snap, err := env.svc.lookupSnapshot(ctx, idGemma)
	if err != nil {
		t.Fatal(err)
	}
	if snap.PID != nil {
		t.Fatalf("pid should be nil after unload, got %v", snap.PID)
	}
	if snap.MemoryMB != nil {
		t.Fatalf("memory_mb should be nil when unloaded")
	}
	fe, err := env.svc.lookupFrontend(ctx, "mlxlm")
	if err != nil {
		t.Fatal(err)
	}
	if len(fe.PIDs) != 0 {
		t.Fatalf("frontend pids = %v", fe.PIDs)
	}
}

func TestMemorySampleTrim(t *testing.T) {
	ctx := context.Background()
	kind := modelstate.FrontendMlxlm
	_, _ = db.Exec(ctx, `DELETE FROM frontend_memory_samples WHERE kind = $1`, kind)

	const keep = 5
	for i := 0; i < 12; i++ {
		if err := insertMemorySample(ctx, kind, int64(100+i), keep); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM frontend_memory_samples WHERE kind = $1`, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != keep {
		t.Fatalf("sample count = %d, want %d", n, keep)
	}
}

func TestSampleFrontendWritesMemory(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx)
	if _, err := env.svc.loadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}
	old := memoryMBFn
	memoryMBFn = func(pids []int64) (int64, error) {
		if len(pids) == 0 {
			t.Fatal("expected pids")
		}
		return 4242, nil
	}
	t.Cleanup(func() { memoryMBFn = old })

	if err := env.svc.sampleFrontend(ctx, modelstate.FrontendMlxlm); err != nil {
		t.Fatal(err)
	}
	fe, err := env.svc.lookupFrontend(ctx, "mlxlm")
	if err != nil {
		t.Fatal(err)
	}
	if fe.MemoryMB == nil || *fe.MemoryMB != 4242 {
		t.Fatalf("memory_mb = %v", fe.MemoryMB)
	}
}

func TestSampleRecoversLostWorker(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx)
	if _, err := env.svc.loadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}
	rt := env.svc.runtime(modelstate.FrontendMlxlm)
	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	// Supervisor ready again with zero workers (crash leaving catalog stale).
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	oldMem := memoryMBFn
	memoryMBFn = func(pids []int64) (int64, error) {
		t.Fatal("should not footprint when runtime reports no live pids")
		return 0, nil
	}
	t.Cleanup(func() { memoryMBFn = oldMem })

	oldBackoff := restartBackoffFor
	restartBackoffFor = func(streak int) time.Duration { return 5 * time.Millisecond }
	t.Cleanup(func() { restartBackoffFor = oldBackoff })

	if err := env.svc.sampleFrontend(ctx, modelstate.FrontendMlxlm); err != nil {
		t.Fatal(err)
	}
	snap := waitObserved(t, env, idGemma, modelstate.ModelLoaded, 3*time.Second)
	if snap.PID == nil || *snap.PID <= 0 {
		t.Fatalf("expected new pid after recover")
	}
	tr, err := isRouteEnabled(ctx, "POST /v1/translations", "structured_translation")
	if err != nil {
		t.Fatal(err)
	}
	if !tr {
		t.Fatal("translations route should be enabled after recover")
	}
}

func TestPIDGoneErr(t *testing.T) {
	err := fmt.Errorf("footprint 8852: exit status 66 (footprint: Unable to find pid for process matching '8852')")
	if !isPIDGoneErr(err) {
		t.Fatal("expected pid-gone detection")
	}
}

func TestParseFootprintOutput(t *testing.T) {
	const sample = `======================================================================
python [67127]: 64-bit    Footprint: 9233 MB (16384 bytes per page)
======================================================================
    phys_footprint: 9233 MB
    phys_footprint_peak: 9715 MB
`
	mb, ok := parseFootprintMB(sample)
	if !ok || mb != 9233 {
		t.Fatalf("got %d ok=%v, want 9233", mb, ok)
	}
	kbSample := `zsh [1]: 64-bit    Footprint: 2224 KB (16384 bytes per page)`
	mb, ok = parseFootprintMB(kbSample)
	if !ok || mb != 2 { // 2224/1024
		t.Fatalf("KB parse got %d ok=%v", mb, ok)
	}
}
