package control

import (
	"context"
	"strconv"
	"sync"
	"time"

	"encore.app/internal/modelstate"
	"encore.dev/rlog"
)

const (
	restartPolicyNo            = "no"
	restartPolicyOnFailure     = "on-failure"
	restartPolicyUnlessStopped = "unless-stopped"
	restartPolicyAlways        = "always"

	defaultOnFailureMaxRetries = 5
	restartCircuitLimit        = 5
	restartBaseBackoff         = time.Second
	restartMaxBackoff          = 60 * time.Second

	// unknownExitCode is used when the sampler detects loss without an exit status.
	// Treated as non-zero so on-failure still schedules a restart.
	unknownExitCode = -1
)

// restartBackoffFor returns exponential backoff for streak (1-based).
// Overridable in tests.
var restartBackoffFor = func(streak int) time.Duration {
	if streak < 1 {
		streak = 1
	}
	d := restartBaseBackoff << (streak - 1)
	if d > restartMaxBackoff {
		return restartMaxBackoff
	}
	return d
}

type restartEntry struct {
	streak  int
	cancel  context.CancelFunc
	pending bool
}

// restartController schedules Docker-style worker restarts after unexpected exits.
type restartController struct {
	svc  *Service
	mu   sync.Mutex
	byID map[string]*restartEntry
}

func newRestartController(svc *Service) *restartController {
	return &restartController{
		svc:  svc,
		byID: make(map[string]*restartEntry),
	}
}

func (rc *restartController) entry(id string) *restartEntry {
	e, ok := rc.byID[id]
	if !ok {
		e = &restartEntry{}
		rc.byID[id] = e
	}
	return e
}

func (rc *restartController) cancel(id string) {
	if rc == nil {
		return
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if e, ok := rc.byID[id]; ok && e.cancel != nil {
		e.cancel()
		e.cancel = nil
		e.pending = false
	}
}

func (rc *restartController) cancelAll() {
	if rc == nil {
		return
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for _, e := range rc.byID {
		if e.cancel != nil {
			e.cancel()
			e.cancel = nil
			e.pending = false
		}
	}
}

func (rc *restartController) resetStreak(id string) {
	if rc == nil {
		return
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if e, ok := rc.byID[id]; ok {
		e.streak = 0
	}
}

// onUnexpectedExit decides whether to schedule a restart for a catalog model.
// observed=failed and route refresh should already have been applied by the caller.
func (rc *restartController) onUnexpectedExit(ctx context.Context, modelID string, exitCode int) {
	if rc == nil {
		return
	}
	row, err := getModel(ctx, modelID)
	if err != nil {
		rlog.Error("restart: model lookup failed",
			"event", "control.restart_lookup_failed",
			"model_id", modelID,
			"err", err,
		)
		return
	}
	policy := row.RestartPolicy
	if policy == "" {
		policy = restartPolicyUnlessStopped
	}

	if row.Desired != modelstate.ModelLoaded || policy == restartPolicyNo {
		return
	}
	if policy == restartPolicyOnFailure && exitCode == 0 {
		return
	}
	// always: same as unless-stopped this slice (still gated by desired=loaded above).
	rc.scheduleRestart(ctx, *row, policy)
}

func (rc *restartController) scheduleRestart(ctx context.Context, row modelRow, policy string) {
	rc.mu.Lock()
	e := rc.entry(row.ID)
	if e.pending {
		rc.mu.Unlock()
		rlog.Info("restart already pending; coalescing",
			"event", "control.restart_coalesced",
			"model_id", row.ID,
		)
		return
	}

	e.streak++
	streak := e.streak
	maxRetries := defaultOnFailureMaxRetries
	if row.RestartMaxRetries != nil && *row.RestartMaxRetries > 0 {
		maxRetries = *row.RestartMaxRetries
	}
	limit := restartCircuitLimit
	if policy == restartPolicyOnFailure {
		limit = maxRetries
	}
	if streak > limit {
		e.pending = false
		rc.mu.Unlock()
		recordEvent(ctx, row.ID, eventRestartGaveUp, map[string]string{
			"streak": strconv.Itoa(streak),
			"policy": policy,
			"limit":  strconv.Itoa(limit),
		})
		rlog.Warn("restart gave up",
			"event", "control.restart_gave_up",
			"model_id", row.ID,
			"streak", streak,
			"policy", policy,
		)
		_ = refreshRouteEnablement(ctx)
		return
	}

	delay := restartBackoffFor(streak)
	timerCtx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.pending = true
	rc.mu.Unlock()

	recordEvent(ctx, row.ID, eventRestartScheduled, map[string]string{
		"streak": strconv.Itoa(streak),
		"delay":  delay.String(),
		"policy": policy,
	})
	rlog.Info("restart scheduled",
		"event", "control.restart_scheduled",
		"model_id", row.ID,
		"streak", streak,
		"delay", delay.String(),
		"policy", policy,
	)

	go rc.runRestart(timerCtx, row.ID, policy, delay)
}

func (rc *restartController) runRestart(timerCtx context.Context, modelID, policy string, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timerCtx.Done():
		rc.mu.Lock()
		if e, ok := rc.byID[modelID]; ok {
			e.pending = false
			e.cancel = nil
		}
		rc.mu.Unlock()
		return
	case <-timer.C:
	}

	rc.mu.Lock()
	if e, ok := rc.byID[modelID]; ok {
		e.pending = false
		e.cancel = nil
	}
	rc.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout+time.Minute)
	defer cancel()

	row, err := getModel(ctx, modelID)
	if err != nil {
		return
	}
	if row.Desired != modelstate.ModelLoaded {
		return
	}
	if row.RestartPolicy == restartPolicyNo {
		return
	}

	rlog.Info("restart attempting load",
		"event", "control.restart_load",
		"model_id", modelID,
		"policy", policy,
	)
	if _, err := rc.svc.loadModelForRestart(ctx, modelID); err != nil {
		rlog.Error("restart load failed",
			"event", "control.restart_load_failed",
			"model_id", modelID,
			"err", err,
		)
		// Re-read for latest policy / desired; schedule again under circuit rules.
		row2, err2 := getModel(ctx, modelID)
		if err2 != nil || row2.Desired != modelstate.ModelLoaded {
			return
		}
		p := row2.RestartPolicy
		if p == "" {
			p = restartPolicyUnlessStopped
		}
		if p == restartPolicyNo {
			return
		}
		rc.scheduleRestart(ctx, *row2, p)
		return
	}
	rc.resetStreak(modelID)
}
