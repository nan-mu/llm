package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	controlv1 "encore.app/control/proto/controlv1"
	"encore.app/internal/modelstate"
	"encore.app/unix"
	"encore.app/unix/llama"
	"encore.app/unix/mlxcel"
	"encore.app/unix/mlxlm"
	"encore.dev/beta/errs"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	idASR   = "fun-asr-nano-2512-q8_0"
	idHY    = "HY-MT2-7B-Q8_0"
	idGemma = "translategemma-12b-it-6bit"
)

type testEnv struct {
	svc        *Service
	llamaSock  string
	mlxcelSock string
	mlxlmSock  string // per-model sock for Gemma under mlxlm cwd
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	bin, err := unix.BuildFakeFrontend()
	if err != nil {
		t.Fatal(err)
	}
	fakemlxBin, err := unix.BuildFakeMlxlm()
	if err != nil {
		t.Fatal(err)
	}
	// macOS AF_UNIX paths cap at 104 bytes; t.TempDir() plus the test name overflows.
	llamaCwd, err := os.MkdirTemp("", "cl-")
	if err != nil {
		t.Fatal(err)
	}
	mlxcelCwd, err := os.MkdirTemp("", "cm-")
	if err != nil {
		t.Fatal(err)
	}
	mlxlmCwd, err := os.MkdirTemp("/tmp", "cx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(llamaCwd)
		_ = os.RemoveAll(mlxcelCwd)
		_ = os.RemoveAll(mlxlmCwd)
	})
	modelsDir := t.TempDir()
	llamaRt, err := llama.NewWithConfig(llama.Config{
		Bin:        bin,
		Cwd:        llamaCwd,
		SocketName: "s.sock",
		ModelsDir:  modelsDir,
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	mlxcelRt, err := mlxcel.NewWithConfig(mlxcel.Config{
		Bin:        bin,
		Cwd:        mlxcelCwd,
		SocketName: "s.sock",
		ModelsDir:  modelsDir,
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	mlxlmRt, err := mlxlm.NewWithConfig(mlxlm.Config{
		Bin:       fakemlxBin,
		Cwd:       mlxlmCwd,
		ModelsDir: modelsDir,
		APIKey:    "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{runtimes: map[modelstate.FrontendKind]unix.Runtime{
		modelstate.FrontendLlama:  llamaRt,
		modelstate.FrontendMlxcel: mlxcelRt,
		modelstate.FrontendMlxlm:  mlxlmRt,
	}}
	svc.restarts = newRestartController(svc)
	t.Cleanup(func() {
		if svc.restarts != nil {
			svc.restarts.cancelAll()
		}
		if svc.grpcSrv != nil {
			svc.grpcSrv.Stop()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = llamaRt.Stop(ctx)
		_ = mlxcelRt.Stop(ctx)
		_ = mlxlmRt.Stop(ctx)
	})
	return &testEnv{
		svc:        svc,
		llamaSock:  filepath.Join(llamaCwd, "s.sock"),
		mlxcelSock: filepath.Join(mlxcelCwd, "s.sock"),
		mlxlmSock:  filepath.Join(mlxlmCwd, idGemma+".sock"),
	}
}

func (e *testEnv) ctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func setDesired(t *testing.T, ctx context.Context, loaded ...string) {
	t.Helper()
	if _, err := db.Exec(ctx, `
		UPDATE models SET desired_state = 'unloaded', observed_state = 'unloaded', last_error = NULL,
		       restart_policy = 'unless-stopped', restart_max_retries = NULL, updated_at = NOW()
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `
		UPDATE frontends SET observed_state = 'stopped', last_error = NULL, updated_at = NOW()
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM model_events`); err != nil {
		t.Fatal(err)
	}
	for _, id := range loaded {
		if _, err := db.Exec(ctx, `UPDATE models SET desired_state = 'loaded' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
}

func sockExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestHealthDoesNotWaitForReconcile(t *testing.T) {
	ctx := context.Background()
	if err := Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
}

func TestRouteEnablementOnLoadUnload(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx)
	if err := env.svc.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	chat, err := isRouteEnabled(ctx, "POST /v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	if chat {
		t.Fatal("chat route should be disabled with no loaded translation model")
	}
	asr, err := isRouteEnabled(ctx, "POST /v1/audio/transcriptions")
	if err != nil {
		t.Fatal(err)
	}
	if asr {
		t.Fatal("asr route should be disabled")
	}

	if _, err := env.svc.loadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}
	chat, err = isRouteEnabled(ctx, "POST /v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	if !chat {
		t.Fatal("chat route should be enabled after loading Gemma")
	}
	asr, err = isRouteEnabled(ctx, "POST /v1/audio/transcriptions")
	if err != nil {
		t.Fatal(err)
	}
	if asr {
		t.Fatal("asr route should stay disabled")
	}

	if _, err := env.svc.unloadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}
	chat, err = isRouteEnabled(ctx, "POST /v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	if chat {
		t.Fatal("chat route should disable after unload")
	}
}

func TestReconcileNoDesiredLoadedStartsNothing(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx)
	if err := env.svc.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if sockExists(env.llamaSock) {
		t.Fatal("llama sock should not exist")
	}
	if sockExists(env.mlxcelSock) {
		t.Fatal("mlxcel sock should not exist")
	}
	if sockExists(env.mlxlmSock) {
		t.Fatal("mlxlm model sock should not exist")
	}
}

func TestReconcileSeedStartsOnlyMlxlm(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx, idGemma)
	if err := env.svc.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if sockExists(env.llamaSock) {
		t.Fatal("llama should not start")
	}
	if sockExists(env.mlxcelSock) {
		t.Fatal("mlxcel should not start")
	}
	if !sockExists(env.mlxlmSock) {
		t.Fatal("expected mlxlm model sock")
	}
	gemma, err := env.svc.lookupSnapshot(ctx, idGemma)
	if err != nil {
		t.Fatal(err)
	}
	if gemma.Observed != string(modelstate.ModelLoaded) {
		t.Fatalf("gemma observed = %s", gemma.Observed)
	}
	if gemma.NativeID != idGemma {
		t.Fatalf("gemma native_id = %s", gemma.NativeID)
	}
	for _, id := range []string{idASR, idHY} {
		snap, err := env.svc.lookupSnapshot(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Observed != string(modelstate.ModelUnloaded) {
			t.Fatalf("%s observed = %s", id, snap.Observed)
		}
	}
}

func TestReconcileSeedStartsOnlyLlama(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx, idASR, idHY)
	if err := env.svc.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if !sockExists(env.llamaSock) {
		t.Fatal("expected llama sock")
	}
	if sockExists(env.mlxcelSock) {
		t.Fatal("mlxcel should not start")
	}
	if sockExists(env.mlxlmSock) {
		t.Fatal("mlxlm should not start")
	}
	for _, id := range []string{idASR, idHY} {
		snap, err := env.svc.lookupSnapshot(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Observed != string(modelstate.ModelLoaded) {
			t.Fatalf("%s observed = %s", id, snap.Observed)
		}
		if snap.NativeID != id {
			t.Fatalf("%s native_id = %s", id, snap.NativeID)
		}
	}
	gemma, err := env.svc.lookupSnapshot(ctx, idGemma)
	if err != nil {
		t.Fatal(err)
	}
	if gemma.Observed != string(modelstate.ModelUnloaded) {
		t.Fatalf("gemma observed = %s", gemma.Observed)
	}
}

func TestAbortBootDrainsFrontends(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx, idGemma)
	if err := env.svc.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if !sockExists(env.mlxlmSock) {
		t.Fatal("expected mlxlm model sock")
	}
	env.svc.abortBoot(context.Canceled)
	if sockExists(env.mlxlmSock) {
		t.Fatal("abortBoot should stop mlxlm workers")
	}
	if sockExists(env.llamaSock) {
		t.Fatal("abortBoot should not leave llama")
	}
}

func TestGRPCLoadGemmaStartsMlxlm(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx, idASR, idHY)
	if err := env.svc.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if sockExists(env.mlxlmSock) {
		t.Fatal("mlxlm should be down before Load")
	}
	if err := env.svc.serveGRPC("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	conn := dialGRPC(t, env.svc.grpcLn.Addr().String())
	defer conn.Close()
	client := controlv1.NewModelControlClient(conn)

	got, err := client.LoadModel(ctx, &controlv1.LoadModelRequest{Id: idGemma})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetObservedState() != string(modelstate.ModelLoaded) {
		t.Fatalf("gemma observed = %s", got.GetObservedState())
	}
	if !sockExists(env.mlxlmSock) {
		t.Fatal("expected mlxlm model sock after Load Gemma")
	}

	_, err = client.GetModel(ctx, &controlv1.GetModelRequest{Id: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("missing model: %v", err)
	}
}

func TestLoadAlreadyLoadedIsIdempotent(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx, idASR, idHY)
	if err := env.svc.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if !sockExists(env.llamaSock) {
		t.Fatal("expected llama sock")
	}
	if _, err := env.svc.loadModel(ctx, idASR); err != nil {
		t.Fatal(err)
	}
	if !sockExists(env.llamaSock) {
		t.Fatal("llama sock disappeared after idempotent load")
	}
	if sockExists(env.mlxcelSock) {
		t.Fatal("idempotent load must not start mlxcel")
	}
	if sockExists(env.mlxlmSock) {
		t.Fatal("idempotent load must not start mlxlm")
	}
}

func TestUnloadLastModelStopsFrontend(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx)
	if _, err := env.svc.loadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}
	if !sockExists(env.mlxlmSock) {
		t.Fatal("expected mlxlm model sock")
	}
	if _, err := env.svc.unloadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}
	if sockExists(env.mlxlmSock) {
		t.Fatal("mlxlm should stop after last unload")
	}
}

func TestUnloadOneOfTwoKeepsFrontend(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx, idASR, idHY)
	if err := env.svc.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.unloadModel(ctx, idASR); err != nil {
		t.Fatal(err)
	}
	if !sockExists(env.llamaSock) {
		t.Fatal("llama should stay up while HY-MT2 remains loaded")
	}
	if sockExists(env.mlxcelSock) {
		t.Fatal("mlxcel should not start")
	}
	if sockExists(env.mlxlmSock) {
		t.Fatal("mlxlm should not start")
	}
	hy, err := env.svc.lookupSnapshot(ctx, idHY)
	if err != nil {
		t.Fatal(err)
	}
	if hy.Observed != string(modelstate.ModelLoaded) {
		t.Fatalf("HY observed = %s", hy.Observed)
	}
}

func TestInsertInfersFrontendFromPath(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM models WHERE id LIKE 'test-%'`)
	})
	if err := insertModel(ctx, "test-gguf", "models/foo.GGUF", "asr", modelstate.ModelUnloaded); err != nil {
		t.Fatal(err)
	}
	gguf, err := getModel(ctx, "test-gguf")
	if err != nil {
		t.Fatal(err)
	}
	if gguf.Frontend != modelstate.FrontendLlama {
		t.Fatalf("gguf frontend = %s", gguf.Frontend)
	}
	if gguf.NativeID != "foo" {
		t.Fatalf("gguf native_id = %s", gguf.NativeID)
	}

	if err := insertModel(ctx, "test-dir", "models/translategemma-dir", "translation", ""); err != nil {
		t.Fatal(err)
	}
	dir, err := getModel(ctx, "test-dir")
	if err != nil {
		t.Fatal(err)
	}
	if dir.Frontend != modelstate.FrontendMlxlm {
		t.Fatalf("dir frontend = %s", dir.Frontend)
	}
	if dir.NativeID != "test-dir" {
		t.Fatalf("dir native_id = %s", dir.NativeID)
	}
}

func TestListDoesNotStartFrontend(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx, idASR, idHY)
	snaps, err := env.svc.listSnapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) < 3 {
		t.Fatalf("catalog len = %d", len(snaps))
	}
	if sockExists(env.llamaSock) || sockExists(env.mlxcelSock) || sockExists(env.mlxlmSock) {
		t.Fatal("list must not start a frontend")
	}
}

func TestGetUnknownModel(t *testing.T) {
	env := newTestEnv(t)
	_, err := env.svc.lookupSnapshot(env.ctx(t), "no-such-model")
	if errs.Code(err) != errs.NotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestReloadKeepsFrontend(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx)
	if _, err := env.svc.loadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.reloadModel(ctx, idGemma); err != nil {
		t.Fatal(err)
	}
	if !sockExists(env.mlxlmSock) {
		t.Fatal("reload must keep mlxlm worker running")
	}
}

func TestReconcileLoadFailureReturnsError(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	env.svc.runtimes[modelstate.FrontendMlxlm] = &failLoadRT{}
	setDesired(t, ctx, idGemma)
	if err := env.svc.reconcile(ctx); err == nil {
		t.Fatal("reconcile should fail when a desired model cannot load")
	}
	gemma, err := env.svc.lookupSnapshot(ctx, idGemma)
	if err != nil {
		t.Fatal(err)
	}
	if gemma.Observed != string(modelstate.ModelFailed) {
		t.Fatalf("gemma observed = %s", gemma.Observed)
	}
	if gemma.Desired != string(modelstate.ModelLoaded) {
		t.Fatalf("gemma desired = %s", gemma.Desired)
	}
}

func TestGRPCLoadFailureKeepsProcess(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	env.svc.runtimes[modelstate.FrontendMlxlm] = &failLoadRT{}
	setDesired(t, ctx)
	if err := env.svc.serveGRPC("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	conn := dialGRPC(t, env.svc.grpcLn.Addr().String())
	defer conn.Close()
	client := controlv1.NewModelControlClient(conn)

	_, err := client.LoadModel(ctx, &controlv1.LoadModelRequest{Id: idGemma})
	if err == nil {
		t.Fatal("LoadModel should return an error")
	}
	got, err := client.GetModel(ctx, &controlv1.GetModelRequest{Id: idGemma})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetObservedState() != string(modelstate.ModelFailed) {
		t.Fatalf("observed = %s", got.GetObservedState())
	}
	if _, err := client.GetModel(ctx, &controlv1.GetModelRequest{Id: idGemma}); err != nil {
		t.Fatalf("process should stay up after Load failure: %v", err)
	}
	backends := controlv1.NewBackendControlClient(conn)
	if _, err := backends.Health(ctx, &controlv1.HealthRequest{}); err != nil {
		t.Fatalf("health after Load failure: %v", err)
	}
}

func TestShutdownUnloadsAndStops(t *testing.T) {
	env := newTestEnv(t)
	ctx := env.ctx(t)
	setDesired(t, ctx, idASR, idHY)
	if err := env.svc.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if !sockExists(env.llamaSock) {
		t.Fatal("expected llama sock")
	}
	force, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	env.svc.Shutdown(force)
	if sockExists(env.llamaSock) {
		t.Fatal("llama sock should be gone after shutdown")
	}
	if sockExists(env.mlxcelSock) {
		t.Fatal("mlxcel sock should be gone after shutdown")
	}
	if sockExists(env.mlxlmSock) {
		t.Fatal("mlxlm sock should be gone after shutdown")
	}
}

type failLoadRT struct {
	started bool
}

func (f *failLoadRT) Start(context.Context) error { f.started = true; return nil }
func (f *failLoadRT) Stop(context.Context) error  { f.started = false; return nil }
func (f *failLoadRT) Ready(context.Context) error {
	if !f.started {
		return unix.ErrNotReady
	}
	return nil
}
func (f *failLoadRT) Load(context.Context, string) error {
	return &unix.Error{Code: unix.CodeLoadFailed, Message: "weight not found"}
}
func (f *failLoadRT) Unload(context.Context, string) error { return nil }
func (f *failLoadRT) Get(context.Context, string) (unix.Model, error) {
	return unix.Model{}, unix.ErrNotReady
}
func (f *failLoadRT) List(context.Context) ([]unix.Model, error) { return nil, nil }
func (f *failLoadRT) EnsureReady(ctx context.Context) error      { return f.Start(ctx) }
func (f *failLoadRT) EnsureLoaded(ctx context.Context, id string) error {
	if err := f.EnsureReady(ctx); err != nil {
		return err
	}
	return f.Load(ctx, id)
}
func (f *failLoadRT) ModelPID(context.Context, string) (int, error) {
	return 0, unix.ErrNotReady
}
func (f *failLoadRT) BackendPIDs(context.Context) ([]int, error) { return nil, nil }

var _ unix.Runtime = (*failLoadRT)(nil)

func dialGRPC(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	client := controlv1.NewBackendControlClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, err := client.Health(ctx, &controlv1.HealthRequest{})
		if err == nil {
			return conn
		}
		if ctx.Err() != nil {
			t.Fatalf("grpc health: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
