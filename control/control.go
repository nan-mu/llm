package control

import (
	"context"

	"encore.app/frontend"
	"encore.app/frontend/llama"
	"encore.app/frontend/mlxcel"
	"encore.app/frontend/mlxlm"
	"encore.app/internal/modelstate"
	"encore.dev/rlog"
)

//encore:service
type Service struct {
	runtimes map[modelstate.FrontendKind]frontend.Runtime
}

func initService() (*Service, error) {
	svc := &Service{
		runtimes: map[modelstate.FrontendKind]frontend.Runtime{
			modelstate.FrontendLlama:  llama.New(),
			modelstate.FrontendMlxcel: mlxcel.New(),
			modelstate.FrontendMlxlm:  mlxlm.New(),
		},
	}
	// Construct runtimes, then reconcile desired=loaded rows.
	// A missing worker is recorded on the catalog row and does not fail init.
	if err := svc.reconcile(context.Background()); err != nil {
		return nil, err
	}
	return svc, nil
}

// HealthResponse is the public control health body.
type HealthResponse struct {
	OK bool `json:"ok"`
}

// Health reports that control and its database are up.
//
//encore:api public method=GET path=/control/health
func (s *Service) Health(ctx context.Context) (*HealthResponse, error) {
	var n int
	if err := db.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		return nil, err
	}
	return &HealthResponse{OK: true}, nil
}

func (s *Service) reconcile(ctx context.Context) error {
	rows, err := listDesiredLoaded(ctx)
	if err != nil {
		return err
	}
	for i := range rows {
		row := &rows[i]
		rt := s.runtimes[row.Frontend]
		if rt == nil {
			if err := markReconcileFailure(ctx, row.Frontend, row.ID, "unknown frontend"); err != nil {
				return err
			}
			continue
		}
		state, startErr := rt.Start(ctx)
		if startErr != nil {
			rlog.Warn("reconcile did not start frontend", "model", row.ID, "frontend", string(row.Frontend), "err", startErr)
			if err := markReconcileFailure(ctx, row.Frontend, row.ID, startErr.Error()); err != nil {
				return err
			}
			continue
		}
		if err := modelstate.CanLoad(state); err != nil {
			if err := markReconcileFailure(ctx, row.Frontend, row.ID, err.Error()); err != nil {
				return err
			}
			continue
		}
		if err := rt.Load(ctx, row.NativeID, row.Path); err != nil {
			rlog.Warn("reconcile load failed", "model", row.ID, "err", err)
			if err := markReconcileFailure(ctx, row.Frontend, row.ID, err.Error()); err != nil {
				return err
			}
			continue
		}
		if err := markLoaded(ctx, row.Frontend, row.ID); err != nil {
			return err
		}
	}
	return refreshRouteEnablement(ctx)
}

func markReconcileFailure(ctx context.Context, kind modelstate.FrontendKind, modelID, msg string) error {
	if _, err := db.Exec(ctx, `
		UPDATE frontends
		SET observed_state = 'failed', last_error = $2, updated_at = NOW()
		WHERE kind = $1
	`, string(kind), msg); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `
		UPDATE models
		SET observed_state = 'failed', last_error = $2, updated_at = NOW()
		WHERE id = $1
	`, modelID, msg)
	return err
}

func markLoaded(ctx context.Context, kind modelstate.FrontendKind, modelID string) error {
	if _, err := db.Exec(ctx, `
		UPDATE frontends
		SET observed_state = 'ready', last_error = NULL, updated_at = NOW()
		WHERE kind = $1
	`, string(kind)); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `
		UPDATE models
		SET observed_state = 'loaded', last_error = NULL, updated_at = NOW()
		WHERE id = $1
	`, modelID)
	return err
}
