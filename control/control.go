package control

import (
	"context"
	"net"
	"os"
	"sync"
	"time"

	"encore.app/internal/modelstate"
	"encore.app/frontend"
	"encore.app/frontend/llama"
	"encore.app/frontend/mlxcel"
	"encore.app/frontend/mlxlm"
	"encore.dev"
	"encore.dev/rlog"
	"encore.dev/storage/sqldb"
	"google.golang.org/grpc"

	controlv1 "encore.app/control/proto/controlv1"
)

const (
	grpcAddr         = "127.0.0.1:9000"
	bootDrainTimeout = 30 * time.Second
)

//encore:service
type Service struct {
	runtimes map[modelstate.FrontendKind]frontend.Runtime
	grpcSrv  *grpc.Server
	grpcLn   net.Listener
	cancel   context.CancelFunc
	restarts *restartController
	mu       sync.Mutex
}

var db = sqldb.NewDatabase("control", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

func initService() (*Service, error) {
	rts, err := newRuntimes()
	if err != nil {
		return nil, err
	}
	s := &Service{runtimes: rts}
	s.restarts = newRestartController(s)
	runCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wireWorkerExitHandlers()
	if err := resetObserved(context.Background()); err != nil {
		cancel()
		return nil, err
	}
	if encore.Meta().Environment.Type == encore.EnvTest {
		return s, nil
	}
	if err := s.serveGRPC(grpcAddr); err != nil {
		cancel()
		return nil, err
	}
	s.startMemorySampler(runCtx)
	if err := s.reconcile(runCtx); err != nil {
		s.abortBoot(err)
		return nil, err
	}
	return s, nil
}

func (s *Service) abortBoot(err error) {
	rlog.Error("startup reconcile failed",
		"event", "control.reconcile_failed",
		"err", err,
	)
	if s.restarts != nil {
		s.restarts.cancelAll()
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), bootDrainTimeout)
	defer cancel()
	s.drainFrontends(drainCtx)
	if s.grpcSrv != nil {
		s.grpcSrv.Stop()
	}
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Service) Shutdown(force context.Context) {
	if s.restarts != nil {
		s.restarts.cancelAll()
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.grpcSrv != nil {
		done := make(chan struct{})
		go func() {
			s.grpcSrv.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-force.Done():
			s.grpcSrv.Stop()
		}
	}
	s.drainFrontends(force)
}

func (s *Service) drainFrontends(ctx context.Context) {
	for _, kind := range modelstate.AllFrontends() {
		rt := s.runtime(kind)
		if rt == nil {
			continue
		}
		s.unloadFrontend(ctx, rt)
		if err := rt.Stop(ctx); err != nil {
			rlog.Error("frontend stop failed",
				"event", "control.frontend_stop_failed",
				"frontend", string(kind),
				"err", err,
			)
		}
		_ = clearFrontendRuntime(ctx, kind)
		_ = setFrontendObserved(ctx, kind, modelstate.FrontendStopped, "")
	}
}

// wireWorkerExitHandlers registers per-model recovery on unexpected worker death
// (does not wait for the 10s memory sampler). Intentional Unload does not fire.
// Restart policy / backoff live in RestartController.
func (s *Service) wireWorkerExitHandlers() {
	type exitAware interface {
		SetWorkerExitHandler(fn func(id string, exitCode int))
	}
	for _, kind := range modelstate.AllFrontends() {
		rt := s.runtime(kind)
		aware, ok := rt.(exitAware)
		if !ok {
			continue
		}
		k := kind
		aware.SetWorkerExitHandler(func(id string, exitCode int) {
			rlog.Warn("worker exit notified",
				"event", "control.worker_exit_notified",
				"frontend", string(k),
				"model_id", id,
				"exit_code", exitCode,
			)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), loadTimeout+time.Minute)
				defer cancel()
				if err := s.handleLostModel(ctx, k, id, exitCode); err != nil {
					rlog.Error("handle lost model after exit failed",
						"event", "control.worker_exit_recover_failed",
						"frontend", string(k),
						"model_id", id,
						"err", err,
					)
				}
			}()
		})
	}
}

func (s *Service) unloadFrontend(ctx context.Context, rt frontend.Runtime) {
	if err := rt.Ready(ctx); err != nil {
		return
	}
	models, err := rt.List(ctx)
	if err != nil {
		rlog.Error("shutdown list models failed",
			"event", "control.shutdown_list_failed",
			"err", err,
		)
		return
	}
	for _, m := range models {
		switch m.State {
		case modelstate.ModelLoaded, modelstate.ModelLoading, modelstate.ModelUnloading:
			if err := rt.Unload(ctx, m.ID); err != nil && !frontend.Is(err, frontend.CodeNotFound) {
				rlog.Error("shutdown unload failed",
					"event", "control.shutdown_unload_failed",
					"model_id", m.ID,
					"err", err,
				)
				continue
			}
			rlog.Info("shutdown model unloaded",
				"event", "control.shutdown_unloaded",
				"model_id", m.ID,
			)
		}
	}
}

// Health reports that the control service is up.
// Outside tests, initService has already finished boot reconcile before this is reachable.
//
//encore:api public method=GET path=/control/health
func (s *Service) Health(ctx context.Context) error {
	var n int
	return db.QueryRow(ctx, "SELECT 1").Scan(&n)
}

func (s *Service) runtime(kind modelstate.FrontendKind) frontend.Runtime {
	if s.runtimes == nil {
		return nil
	}
	return s.runtimes[kind]
}

func (s *Service) serveGRPC(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.grpcLn = ln
	s.grpcSrv = grpc.NewServer()
	api := &grpcAPI{svc: s}
	controlv1.RegisterModelControlServer(s.grpcSrv, api)
	controlv1.RegisterBackendControlServer(s.grpcSrv, api)
	go func() {
		if err := s.grpcSrv.Serve(ln); err != nil && err != grpc.ErrServerStopped {
			rlog.Error("grpc server stopped",
				"event", "control.grpc_stopped",
				"err", err,
			)
		}
	}()
	return nil
}

func newRuntimes() (map[modelstate.FrontendKind]frontend.Runtime, error) {
	if encore.Meta().Environment.Type == encore.EnvTest {
		return newFakeuxRuntimes()
	}
	llamaRt, err := llama.New()
	if err != nil {
		return nil, err
	}
	mlxcelRt, err := mlxcel.New()
	if err != nil {
		return nil, err
	}
	mlxlmRt, err := mlxlm.New()
	if err != nil {
		return nil, err
	}
	return map[modelstate.FrontendKind]frontend.Runtime{
		modelstate.FrontendLlama:  llamaRt,
		modelstate.FrontendMlxcel: mlxcelRt,
		modelstate.FrontendMlxlm:  mlxlmRt,
	}, nil
}

func newFakeuxRuntimes() (map[modelstate.FrontendKind]frontend.Runtime, error) {
	bin, err := frontend.BuildFakeFrontend()
	if err != nil {
		return nil, err
	}
	fakemlxBin, err := frontend.BuildFakeMlxlm()
	if err != nil {
		return nil, err
	}
	llamaCwd, err := os.MkdirTemp("", "cl-")
	if err != nil {
		return nil, err
	}
	mlxcelCwd, err := os.MkdirTemp("", "cm-")
	if err != nil {
		return nil, err
	}
	mlxlmCwd, err := os.MkdirTemp("/tmp", "cx-")
	if err != nil {
		return nil, err
	}
	modelsDir, err := os.MkdirTemp("", "cmod-")
	if err != nil {
		return nil, err
	}
	llamaRt, err := llama.NewWithConfig(llama.Config{
		Bin:       bin,
		Cwd:       llamaCwd,
		ModelsDir: modelsDir,
		APIKey:    "test-key",
	})
	if err != nil {
		return nil, err
	}
	mlxcelRt, err := mlxcel.NewWithConfig(mlxcel.Config{
		Bin:        bin,
		Cwd:        mlxcelCwd,
		SocketName: "s.sock",
		ModelsDir:  modelsDir,
		APIKey:     "test-key",
	})
	if err != nil {
		return nil, err
	}
	mlxlmRt, err := mlxlm.NewWithConfig(mlxlm.Config{
		Bin:       fakemlxBin,
		Cwd:       mlxlmCwd,
		ModelsDir: modelsDir,
		APIKey:    "test-key",
	})
	if err != nil {
		return nil, err
	}
	return map[modelstate.FrontendKind]frontend.Runtime{
		modelstate.FrontendLlama:  llamaRt,
		modelstate.FrontendMlxcel: mlxcelRt,
		modelstate.FrontendMlxlm:  mlxlmRt,
	}, nil
}
