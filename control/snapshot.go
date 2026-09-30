package control

import (
	"context"

	"encore.app/internal/modelstate"
	"encore.dev/beta/errs"
)

// Snapshot 是目录中一个模型的快照（含可选的 live unix 观测）。
type Snapshot struct {
	// 目录 ID（网关请求中的 model）
	ID string `json:"id"`
	// 所属 frontend：llama / mlxcel / mlxlm
	Frontend string `json:"frontend"`
	// 权重路径
	Path string `json:"path"`
	// 用途：translation / structured_translation / asr 等
	Purpose string `json:"purpose"`
	// Frontend 侧原生 ID
	NativeID string `json:"native_id"`
	// 期望驻留：loaded / unloaded
	Desired string `json:"desired_state"`
	// 观测驻留：unloaded / loading / loaded / unloading / failed
	Observed string `json:"observed_state"`
	// Unix socket（supervisor 型 frontend 通常省略）
	SocketPath *string `json:"socket_path,omitempty"`
	// 最近错误信息
	LastError string `json:"last_error,omitempty"`
	// 已加载时的工作进程 PID
	PID *int64 `json:"pid,omitempty"`
	// 已加载时的理论内存占用（MB）
	MemoryMB *int64 `json:"memory_mb,omitempty"`
}

// GetSnapshot 返回单个模型快照。只读，不启动 frontend。
//
//encore:api public method=GET path=/control/models/:id
func (s *Service) GetSnapshot(ctx context.Context, id string) (*Snapshot, error) {
	return s.lookupSnapshot(ctx, id)
}

// ListSnapshotsResponse 是模型目录列表响应。
type ListSnapshotsResponse struct {
	// 全部模型快照
	Models []Snapshot `json:"models"`
}

// ListSnapshots 列出目录中全部模型。只读，不启动 frontend。
//
//encore:api public method=GET path=/control/models
func (s *Service) ListSnapshots(ctx context.Context) (*ListSnapshotsResponse, error) {
	models, err := s.listSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	return &ListSnapshotsResponse{Models: models}, nil
}

func (s *Service) lookupSnapshot(ctx context.Context, id string) (*Snapshot, error) {
	row, err := getModel(ctx, id)
	if err != nil {
		return nil, err
	}
	snap := row.snapshot()
	s.overlayLive(ctx, snap)
	return snap, nil
}

func (s *Service) listSnapshots(ctx context.Context) ([]Snapshot, error) {
	rows, err := listModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Snapshot, 0, len(rows))
	for i := range rows {
		snap := rows[i].snapshot()
		s.overlayLive(ctx, snap)
		out = append(out, *snap)
	}
	return out, nil
}

func (s *Service) overlayLive(ctx context.Context, snap *Snapshot) {
	rt := s.runtime(modelstate.FrontendKind(snap.Frontend))
	if rt == nil {
		return
	}
	if err := rt.Ready(ctx); err != nil {
		return
	}
	live, err := rt.Get(ctx, snap.NativeID)
	if err != nil {
		return
	}
	snap.Observed = string(live.State)
}

// ListFrontendsResponse 是 frontend 列表响应。
type ListFrontendsResponse struct {
	// 全部 frontend 快照
	Frontends []FrontendSnapshot `json:"frontends"`
}

// ListFrontends 列出全部 frontend（ready 以目录 observed_state 为准）。
//
//encore:api public method=GET path=/control/frontends
func (s *Service) ListFrontends(ctx context.Context) (*ListFrontendsResponse, error) {
	snaps, err := s.frontendSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	return &ListFrontendsResponse{Frontends: snaps}, nil
}

// GetFrontend 按 kind 返回单个 frontend。
//
//encore:api public method=GET path=/control/frontends/:kind
func (s *Service) GetFrontend(ctx context.Context, kind string) (*FrontendSnapshot, error) {
	return s.lookupFrontend(ctx, kind)
}

func (s *Service) frontendSnapshots(ctx context.Context) ([]FrontendSnapshot, error) {
	rows, err := listFrontends(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FrontendSnapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.frontendSnapshot(ctx, row))
	}
	return out, nil
}

func (s *Service) lookupFrontend(ctx context.Context, kind string) (*FrontendSnapshot, error) {
	k := modelstate.FrontendKind(kind)
	if !modelstate.ValidFrontend(k) {
		return nil, errFrontendNotFound()
	}
	row, err := getFrontend(ctx, k)
	if err != nil {
		return nil, err
	}
	snap := s.frontendSnapshot(ctx, *row)
	return &snap, nil
}

func (s *Service) frontendSnapshot(ctx context.Context, row frontendRow) FrontendSnapshot {
	// ready follows catalog observed_state, not merely frontend.Ready (mlxlm is
	// Ready after Start with zero workers — that must not look ready in Health).
	ready := row.Observed == modelstate.FrontendReady
	return FrontendSnapshot{
		Kind:       string(row.Kind),
		SocketPath: row.SocketPath,
		Observed:   string(row.Observed),
		LastError:  row.LastError,
		Ready:      ready,
		PIDs:       row.PIDs,
		MemoryMB:   row.MemoryMB,
	}
}

// FrontendSnapshot 是管理面返回的单个 frontend 状态
//（Ready 跟随目录 observed_state，而非仅表示进程已 Start）。
type FrontendSnapshot struct {
	// frontend 种类：llama / mlxcel / mlxlm
	Kind string `json:"kind"`
	// 共享 Unix socket（若有）
	SocketPath *string `json:"socket_path,omitempty"`
	// 观测状态：stopped / starting / loading / ready / stopping / failed
	Observed string `json:"observed_state"`
	// 最近错误
	LastError string `json:"last_error,omitempty"`
	// 是否就绪（目录 observed_state == ready）
	Ready bool `json:"ready"`
	// 相关进程 PID
	PIDs []int64 `json:"pids,omitempty"`
	// 采样内存（MB）
	MemoryMB *int64 `json:"memory_mb,omitempty"`
}

func (row modelRow) snapshot() *Snapshot {
	sock := row.SocketPath
	if modelstate.SupervisorFrontend(row.Frontend) {
		sock = nil
	}
	snap := &Snapshot{
		ID:         row.ID,
		Frontend:   string(row.Frontend),
		Path:       row.Path,
		Purpose:    string(row.Purpose),
		NativeID:   row.NativeID,
		Desired:    string(row.Desired),
		Observed:   string(row.Observed),
		SocketPath: sock,
		LastError:  row.LastError,
		PID:        row.PID,
	}
	if row.Observed == modelstate.ModelLoaded {
		if mb, ok := theoreticalMemoryMB(row.ID); ok {
			v := mb
			snap.MemoryMB = &v
		}
	}
	return snap
}

func (snap *Snapshot) asModelstate() modelstate.ModelSnapshot {
	sock := ""
	if snap.SocketPath != nil {
		sock = *snap.SocketPath
	}
	out := modelstate.ModelSnapshot{
		ID:         snap.ID,
		Frontend:   modelstate.FrontendKind(snap.Frontend),
		Path:       snap.Path,
		Purpose:    modelstate.Purpose(snap.Purpose),
		NativeID:   snap.NativeID,
		Desired:    modelstate.ModelState(snap.Desired),
		Observed:   modelstate.ModelState(snap.Observed),
		SocketPath: sock,
		LastError:  snap.LastError,
		PID:        snap.PID,
		MemoryMB:   snap.MemoryMB,
	}
	return out
}

// Get implements modelstate.Reader. It does not start a frontend.
func (s *Service) Get(id string) (modelstate.ModelSnapshot, bool) {
	snap, err := s.lookupSnapshot(context.Background(), id)
	if err != nil {
		return modelstate.ModelSnapshot{}, false
	}
	return snap.asModelstate(), true
}

// All implements modelstate.Reader. It does not start a frontend.
func (s *Service) All() []modelstate.ModelSnapshot {
	snaps, err := s.listSnapshots(context.Background())
	if err != nil {
		return nil
	}
	out := make([]modelstate.ModelSnapshot, 0, len(snaps))
	for i := range snaps {
		out = append(out, snaps[i].asModelstate())
	}
	return out
}

func errFrontendNotFound() error {
	return &errs.Error{Code: errs.NotFound, Message: "frontend not found"}
}

var _ modelstate.Reader = (*Service)(nil)
