package control

import (
	"context"

	"encore.app/internal/modelstate"
	"encore.dev/beta/errs"
)

// Snapshot is the catalog + optional live unix view of one model.
type Snapshot struct {
	ID         string  `json:"id"`
	Frontend   string  `json:"frontend"`
	Path       string  `json:"path"`
	Purpose    string  `json:"purpose"`
	NativeID   string  `json:"native_id"`
	Desired    string  `json:"desired_state"`
	Observed   string  `json:"observed_state"`
	SocketPath *string `json:"socket_path,omitempty"`
	LastError  string  `json:"last_error,omitempty"`
	PID        *int64  `json:"pid,omitempty"`
	MemoryMB   *int64  `json:"memory_mb,omitempty"`
}

// GetSnapshot returns one catalog row. It does not start a frontend.
//
//encore:api private method=GET path=/control/models/:id
func (s *Service) GetSnapshot(ctx context.Context, id string) (*Snapshot, error) {
	return s.lookupSnapshot(ctx, id)
}

// ListSnapshotsResponse is the private catalog listing.
type ListSnapshotsResponse struct {
	Models []Snapshot `json:"models"`
}

// ListSnapshots returns catalog rows. It does not start a frontend.
//
//encore:api private method=GET path=/control/models
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

func (s *Service) backendSnapshots(ctx context.Context) ([]BackendSnapshot, error) {
	rows, err := listFrontends(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BackendSnapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.backendSnapshot(ctx, row))
	}
	return out, nil
}

func (s *Service) lookupBackend(ctx context.Context, kind string) (*BackendSnapshot, error) {
	k := modelstate.FrontendKind(kind)
	if !modelstate.ValidFrontend(k) {
		return nil, errFrontendNotFound()
	}
	row, err := getFrontend(ctx, k)
	if err != nil {
		return nil, err
	}
	snap := s.backendSnapshot(ctx, *row)
	return &snap, nil
}

func (s *Service) backendSnapshot(ctx context.Context, row frontendRow) BackendSnapshot {
	// ready follows catalog observed_state, not merely frontend.Ready (mlxlm is
	// Ready after Start with zero workers — that must not look ready in Health).
	ready := row.Observed == modelstate.FrontendReady
	return BackendSnapshot{
		Kind:       string(row.Kind),
		SocketPath: row.SocketPath,
		Observed:   string(row.Observed),
		LastError:  row.LastError,
		Ready:      ready,
		PIDs:       row.PIDs,
		MemoryMB:   row.MemoryMB,
	}
}

// BackendSnapshot is one frontend as reported to gRPC (Ready is live, not Start).
type BackendSnapshot struct {
	Kind       string
	SocketPath *string
	Observed   string
	LastError  string
	Ready      bool
	PIDs       []int64
	MemoryMB   *int64
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
