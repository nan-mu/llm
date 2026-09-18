package control

import (
	"context"
	"testing"
	"time"

	"encore.app/internal/modelstate"
)

func waitObserved(t *testing.T, env *testEnv, id string, want modelstate.ModelState, timeout time.Duration) *Snapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last *Snapshot
	for time.Now().Before(deadline) {
		snap, err := env.svc.lookupSnapshot(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		last = snap
		if snap.Observed == string(want) {
			if want == modelstate.ModelLoaded && (snap.PID == nil || *snap.PID <= 0) {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			return snap
		}
		time.Sleep(20 * time.Millisecond)
	}
	if last == nil {
		t.Fatalf("no snapshot for %s", id)
	}
	t.Fatalf("timed out waiting for observed=%s, last=%s pid=%v", want, last.Observed, last.PID)
	return last
}

func countModelEvents(t *testing.T, ctx context.Context, modelID, event string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM model_events WHERE model = $1 AND event = $2
	`, modelID, event).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func setRestartPolicy(t *testing.T, ctx context.Context, id, policy string, maxRetries *int) {
	t.Helper()
	if maxRetries == nil {
		if _, err := db.Exec(ctx, `
			UPDATE models SET restart_policy = $2, restart_max_retries = NULL, updated_at = NOW()
			WHERE id = $1
		`, id, policy); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := db.Exec(ctx, `
		UPDATE models SET restart_policy = $2, restart_max_retries = $3, updated_at = NOW()
		WHERE id = $1
	`, id, policy, *maxRetries); err != nil {
		t.Fatal(err)
	}
}

func TestRestartPolicyNoDoesNotReload(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx)
	setRestartPolicy(t, ctx, idGemma, restartPolicyNo, nil)
	if _, err := env.svc.loadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}

	rt := env.svc.runtime(modelstate.FrontendMlxlm)
	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	oldBackoff := restartBackoffFor
	restartBackoffFor = func(streak int) time.Duration { return 5 * time.Millisecond }
	t.Cleanup(func() { restartBackoffFor = oldBackoff })

	if err := env.svc.sampleFrontend(ctx, modelstate.FrontendMlxlm); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	snap, err := env.svc.lookupSnapshot(ctx, idGemma)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Observed != string(modelstate.ModelFailed) {
		t.Fatalf("observed=%s, want failed", snap.Observed)
	}
	if countModelEvents(t, ctx, idGemma, eventRestartScheduled) != 0 {
		t.Fatal("policy=no must not schedule restart")
	}
}

func TestUnloadCancelsPendingRestart(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx)
	if _, err := env.svc.loadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}

	oldBackoff := restartBackoffFor
	restartBackoffFor = func(streak int) time.Duration { return 2 * time.Second }
	t.Cleanup(func() { restartBackoffFor = oldBackoff })

	rt := env.svc.runtime(modelstate.FrontendMlxlm)
	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.sampleFrontend(ctx, modelstate.FrontendMlxlm); err != nil {
		t.Fatal(err)
	}
	if countModelEvents(t, ctx, idGemma, eventRestartScheduled) < 1 {
		t.Fatal("expected restart scheduled")
	}

	if _, err := env.svc.unloadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	env.svc.restarts.mu.Lock()
	e := env.svc.restarts.byID[idGemma]
	pending := e != nil && e.pending
	env.svc.restarts.mu.Unlock()
	if pending {
		t.Fatal("pending restart should be cancelled after Unload")
	}

	time.Sleep(300 * time.Millisecond)
	snap, err := env.svc.lookupSnapshot(ctx, idGemma)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Observed == string(modelstate.ModelLoaded) {
		t.Fatal("Unload must not allow auto-restart to reload")
	}
	if snap.Desired != string(modelstate.ModelUnloaded) {
		t.Fatalf("desired=%s", snap.Desired)
	}
}

func TestRestartGaveUpOnFailure(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx)
	max := 2
	setRestartPolicy(t, ctx, idGemma, restartPolicyOnFailure, &max)

	env.svc.restarts.mu.Lock()
	env.svc.restarts.byID[idGemma] = &restartEntry{streak: 2}
	env.svc.restarts.mu.Unlock()

	_ = setModelObserved(ctx, idGemma, modelstate.ModelFailed, "worker process gone")
	_ = setModelDesired(ctx, idGemma, modelstate.ModelLoaded)

	env.svc.restarts.onUnexpectedExit(ctx, idGemma, 1)
	time.Sleep(50 * time.Millisecond)

	if countModelEvents(t, ctx, idGemma, eventRestartGaveUp) < 1 {
		t.Fatal("expected RESTART_GAVE_UP")
	}
	env.svc.restarts.mu.Lock()
	pending := env.svc.restarts.byID[idGemma] != nil && env.svc.restarts.byID[idGemma].pending
	env.svc.restarts.mu.Unlock()
	if pending {
		t.Fatal("must not leave pending restart after give-up")
	}
}

func TestCancelAllAbortsPendingRestart(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx)
	if _, err := env.svc.loadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}

	oldBackoff := restartBackoffFor
	restartBackoffFor = func(streak int) time.Duration { return 2 * time.Second }
	t.Cleanup(func() { restartBackoffFor = oldBackoff })

	rt := env.svc.runtime(modelstate.FrontendMlxlm)
	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := env.svc.sampleFrontend(ctx, modelstate.FrontendMlxlm); err != nil {
		t.Fatal(err)
	}

	env.svc.restarts.cancelAll()
	time.Sleep(100 * time.Millisecond)

	snap, err := env.svc.lookupSnapshot(ctx, idGemma)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Observed == string(modelstate.ModelLoaded) {
		t.Fatal("cancelAll must prevent restart load")
	}
}

func TestRestartBackoffCap(t *testing.T) {
	d := restartBackoffFor(1)
	if d != time.Second {
		t.Fatalf("streak1 = %v", d)
	}
	d = restartBackoffFor(2)
	if d != 2*time.Second {
		t.Fatalf("streak2 = %v", d)
	}
	d = restartBackoffFor(10)
	if d != restartMaxBackoff {
		t.Fatalf("streak10 = %v, want cap %v", d, restartMaxBackoff)
	}
}

func TestOnFailureZeroExitDoesNotRestart(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx)
	setRestartPolicy(t, ctx, idGemma, restartPolicyOnFailure, nil)
	_ = setModelDesired(ctx, idGemma, modelstate.ModelLoaded)
	_ = setModelObserved(ctx, idGemma, modelstate.ModelFailed, "clean exit")

	env.svc.restarts.onUnexpectedExit(ctx, idGemma, 0)
	time.Sleep(50 * time.Millisecond)

	if countModelEvents(t, ctx, idGemma, eventRestartScheduled) != 0 {
		t.Fatal("on-failure with exit 0 must not schedule")
	}
}
