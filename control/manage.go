package control

import (
	"context"

	"encore.app/internal/modelstate"
	"encore.dev/beta/errs"
)

// DesiredStateParams 设置单个模型的期望驻留状态。
type DesiredStateParams struct {
	// 期望状态：loaded（加载）或 unloaded（卸载）
	State string `json:"state"`
}

// SetDesiredState 按期望驻留加载或卸载模型（幂等）。
//
//encore:api public method=PUT path=/control/models/:id/desired-state
func (s *Service) SetDesiredState(ctx context.Context, id string, p *DesiredStateParams) (*Snapshot, error) {
	switch modelstate.ModelState(p.State) {
	case modelstate.ModelLoaded:
		return s.loadModel(ctx, id)
	case modelstate.ModelUnloaded:
		return s.unloadModel(ctx, id)
	default:
		return nil, &errs.Error{
			Code:    errs.InvalidArgument,
			Message: "state must be loaded or unloaded",
		}
	}
}

// CreateReload 创建一次重载（先卸载再加载）。
//
//encore:api public method=POST path=/control/models/:id/reloads
func (s *Service) CreateReload(ctx context.Context, id string) (*Snapshot, error) {
	return s.reloadModel(ctx, id)
}
