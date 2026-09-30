package control

import (
	"context"
	"fmt"
	"time"

	"encore.app/internal/modelstate"
	"encore.app/unix"
	"encore.dev/rlog"
)

const (
	loadTimeout  = 5 * time.Minute
	startTimeout = 45 * time.Second
	stopTimeout  = 5 * time.Second
)

func (s *Service) reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	recordEvent(ctx, "", eventReconcileStart, nil)
	wanted, err := listDesiredLoaded(ctx)
	if err != nil {
		recordEvent(ctx, "", eventReconcileFailed, map[string]string{"error": err.Error()})
		return err
	}
	groups := map[modelstate.FrontendKind][]modelRow{}
	for _, row := range wanted {
		groups[row.Frontend] = append(groups[row.Frontend], row)
	}
	for _, kind := range modelstate.AllFrontends() {
		models := groups[kind]
		if len(models) == 0 {
			continue
		}
		if err := s.ensureFrontendLocked(ctx, kind); err != nil {
			rlog.Error("reconcile frontend start failed",
				"event", "control.frontend_start_failed",
				"frontend", string(kind),
				"err", err,
			)
			for _, row := range models {
				_ = setModelObserved(ctx, row.ID, modelstate.ModelFailed, err.Error())
				recordEvent(ctx, row.ID, eventLoadFailed, map[string]string{
					"error":    err.Error(),
					"frontend": string(kind),
				})
			}
			recordEvent(ctx, "", eventReconcileFailed, map[string]string{
				"error":    err.Error(),
				"frontend": string(kind),
			})
			return fmt.Errorf("reconcile start %s: %w", kind, err)
		}
		for _, row := range models {
			if _, err := s.applyLoadLocked(ctx, row); err != nil {
				rlog.Error("reconcile load failed",
					"event", "control.reconcile_load_failed",
					"model_id", row.ID,
					"err", err,
				)
				recordEvent(ctx, "", eventReconcileFailed, map[string]string{
					"error":    err.Error(),
					"model_id": row.ID,
				})
				return fmt.Errorf("reconcile load %s: %w", row.ID, err)
			}
		}
	}
	if err := refreshRouteEnablement(ctx); err != nil {
		recordEvent(ctx, "", eventReconcileFailed, map[string]string{"error": err.Error()})
		return fmt.Errorf("reconcile route enablement: %w", err)
	}
	recordEvent(ctx, "", eventReconcileDone, nil)
	return nil
}

func (s *Service) loadModel(ctx context.Context, id string) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row, err := getModel(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := setModelDesired(ctx, id, modelstate.ModelLoaded); err != nil {
		return nil, err
	}
	row.Desired = modelstate.ModelLoaded
	// Explicit Load clears circuit so operators can recover after RESTART_GAVE_UP.
	if s.restarts != nil {
		s.restarts.resetStreak(id)
	}
	recordEvent(ctx, id, eventLoad, map[string]string{"native_id": row.NativeID})
	snap, err := s.applyLoadLocked(ctx, *row)
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// loadModelForRestart is used by RestartController; it does not clear streak on entry.
func (s *Service) loadModelForRestart(ctx context.Context, id string) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row, err := getModel(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := setModelDesired(ctx, id, modelstate.ModelLoaded); err != nil {
		return nil, err
	}
	row.Desired = modelstate.ModelLoaded
	recordEvent(ctx, id, eventLoad, map[string]string{"native_id": row.NativeID, "restart": "true"})
	return s.applyLoadLocked(ctx, *row)
}

func (s *Service) applyLoadLocked(ctx context.Context, row modelRow) (*Snapshot, error) {
	rt := s.runtime(row.Frontend)
	if rt == nil {
		err := fmt.Errorf("no runtime for frontend %s", row.Frontend)
		_ = setModelObserved(ctx, row.ID, modelstate.ModelFailed, err.Error())
		recordEvent(ctx, row.ID, eventLoadFailed, map[string]string{"error": err.Error()})
		return nil, err
	}
	if row.Observed == modelstate.ModelLoaded {
		if err := rt.Ready(ctx); err == nil {
			if live, err := rt.Get(ctx, row.NativeID); err == nil && live.State == modelstate.ModelLoaded {
				if row.PID == nil {
					if pid, err := rt.ModelPID(ctx, row.NativeID); err == nil && pid > 0 {
						p := int64(pid)
						_ = setModelPID(ctx, row.ID, &p)
						row.PID = &p
						_ = refreshFrontendPIDs(ctx, row.Frontend)
					}
				}
				_ = refreshRouteEnablement(ctx)
				return row.snapshot(), nil
			}
		}
	}

	if err := s.ensureFrontendLocked(ctx, row.Frontend); err != nil {
		_ = setModelObserved(ctx, row.ID, modelstate.ModelFailed, err.Error())
		recordEvent(ctx, row.ID, eventLoadFailed, map[string]string{"error": err.Error()})
		return nil, err
	}

	st := modelstate.FrontendStopped
	if err := rt.Ready(ctx); err == nil {
		st = modelstate.FrontendReady
	}
	if err := modelstate.CanLoad(st); err != nil {
		_ = setModelObserved(ctx, row.ID, modelstate.ModelFailed, err.Error())
		recordEvent(ctx, row.ID, eventLoadFailed, map[string]string{"error": err.Error()})
		return nil, err
	}

	if err := setFrontendObserved(ctx, row.Frontend, modelstate.FrontendLoading, ""); err != nil {
		return nil, err
	}
	if err := setModelObserved(ctx, row.ID, modelstate.ModelLoading, ""); err != nil {
		return nil, err
	}
	loadCtx, cancel := withTimeout(ctx, loadTimeout)
	defer cancel()
	if err := rt.Load(loadCtx, row.NativeID); err != nil {
		_ = setModelObserved(ctx, row.ID, modelstate.ModelFailed, err.Error())
		_ = setFrontendObserved(ctx, row.Frontend, modelstate.FrontendFailed, err.Error())
		recordEvent(ctx, row.ID, eventLoadFailed, map[string]string{"error": err.Error()})
		return nil, err
	}
	if err := setModelObserved(ctx, row.ID, modelstate.ModelLoaded, ""); err != nil {
		return nil, err
	}
	if pid, err := rt.ModelPID(ctx, row.NativeID); err == nil && pid > 0 {
		p := int64(pid)
		_ = setModelPID(ctx, row.ID, &p)
		row.PID = &p
	}
	_ = refreshFrontendPIDs(ctx, row.Frontend)
	if err := setFrontendObserved(ctx, row.Frontend, modelstate.FrontendReady, ""); err != nil {
		return nil, err
	}
	recordEvent(ctx, row.ID, eventLoaded, map[string]string{"native_id": row.NativeID})
	row.Observed = modelstate.ModelLoaded
	row.LastError = ""
	_ = refreshRouteEnablement(ctx)
	return row.snapshot(), nil
}

func (s *Service) unloadModel(ctx context.Context, id string) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unloadLocked(ctx, id, true)
}

func (s *Service) reloadModel(ctx context.Context, id string) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.unloadLocked(ctx, id, false); err != nil {
		return nil, err
	}
	row, err := getModel(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := setModelDesired(ctx, id, modelstate.ModelLoaded); err != nil {
		return nil, err
	}
	row.Desired = modelstate.ModelLoaded
	recordEvent(ctx, id, eventLoad, map[string]string{"native_id": row.NativeID, "reload": "true"})
	return s.applyLoadLocked(ctx, *row)
}

func (s *Service) unloadLocked(ctx context.Context, id string, stopIfIdle bool) (*Snapshot, error) {
	row, err := getModel(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := setModelDesired(ctx, id, modelstate.ModelUnloaded); err != nil {
		return nil, err
	}
	row.Desired = modelstate.ModelUnloaded
	if s.restarts != nil {
		s.restarts.cancel(id)
	}
	recordEvent(ctx, id, eventUnload, map[string]string{"native_id": row.NativeID})

	rt := s.runtime(row.Frontend)
	if rt != nil && rt.Ready(ctx) == nil {
		if err := setModelObserved(ctx, id, modelstate.ModelUnloading, ""); err != nil {
			return nil, err
		}
		unloadCtx, cancel := withTimeout(ctx, loadTimeout)
		defer cancel()
		if err := rt.Unload(unloadCtx, row.NativeID); err != nil && !unix.Is(err, unix.CodeNotFound) {
			_ = setModelObserved(ctx, id, modelstate.ModelFailed, err.Error())
			recordEvent(ctx, id, eventUnloadFailed, map[string]string{"error": err.Error()})
			return nil, err
		}
	}

	if err := setModelObserved(ctx, id, modelstate.ModelUnloaded, ""); err != nil {
		return nil, err
	}
	_ = setModelPID(ctx, id, nil)
	_ = refreshFrontendPIDs(ctx, row.Frontend)
	recordEvent(ctx, id, eventUnloaded, map[string]string{"native_id": row.NativeID})
	row.Observed = modelstate.ModelUnloaded
	row.LastError = ""
	row.PID = nil

	if stopIfIdle {
		if err := s.maybeStopFrontendLocked(ctx, row.Frontend); err != nil {
			rlog.Error("idle frontend stop failed",
				"event", "control.frontend_stop_failed",
				"frontend", string(row.Frontend),
				"err", err,
			)
		}
	}
	_ = refreshRouteEnablement(ctx)
	return row.snapshot(), nil
}

func (s *Service) ensureFrontendLocked(ctx context.Context, kind modelstate.FrontendKind) error {
	rt := s.runtime(kind)
	if rt == nil {
		return fmt.Errorf("no runtime for frontend %s", kind)
	}
	if err := rt.Ready(ctx); err == nil {
		// Supervisor Start is a no-op ready flag: stay loading until a model finishes Load.
		if modelstate.SupervisorFrontend(kind) {
			return setFrontendObserved(ctx, kind, modelstate.FrontendLoading, "")
		}
		return setFrontendObserved(ctx, kind, modelstate.FrontendReady, "")
	}
	if err := setFrontendObserved(ctx, kind, modelstate.FrontendStarting, ""); err != nil {
		return err
	}
	startCtx, cancel := withTimeout(ctx, startTimeout)
	defer cancel()
	if err := rt.Start(startCtx); err != nil {
		_ = setFrontendObserved(ctx, kind, modelstate.FrontendFailed, err.Error())
		recordEvent(ctx, "", eventFrontendStart, map[string]string{
			"frontend": string(kind),
			"error":    err.Error(),
		})
		return err
	}
	if err := rt.Ready(ctx); err != nil {
		_ = setFrontendObserved(ctx, kind, modelstate.FrontendFailed, err.Error())
		recordEvent(ctx, "", eventFrontendStart, map[string]string{
			"frontend": string(kind),
			"error":    err.Error(),
		})
		return err
	}
	afterStart := modelstate.FrontendReady
	if modelstate.SupervisorFrontend(kind) {
		afterStart = modelstate.FrontendLoading
	}
	if err := setFrontendObserved(ctx, kind, afterStart, ""); err != nil {
		return err
	}
	recordEvent(ctx, "", eventFrontendStart, map[string]string{"frontend": string(kind)})
	return nil
}

func (s *Service) maybeStopFrontendLocked(ctx context.Context, kind modelstate.FrontendKind) error {
	n, err := countActiveOnFrontend(ctx, kind)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	rt := s.runtime(kind)
	if rt == nil || rt.Ready(ctx) != nil {
		_ = clearFrontendRuntime(ctx, kind)
		return setFrontendObserved(ctx, kind, modelstate.FrontendStopped, "")
	}
	if err := setFrontendObserved(ctx, kind, modelstate.FrontendStopping, ""); err != nil {
		return err
	}
	stopCtx, cancel := withTimeout(ctx, stopTimeout)
	defer cancel()
	if err := rt.Stop(stopCtx); err != nil {
		_ = setFrontendObserved(ctx, kind, modelstate.FrontendFailed, err.Error())
		return err
	}
	_ = clearFrontendRuntime(ctx, kind)
	if err := setFrontendObserved(ctx, kind, modelstate.FrontendStopped, ""); err != nil {
		return err
	}
	recordEvent(ctx, "", eventFrontendStop, map[string]string{"frontend": string(kind)})
	return nil
}

func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}
