package control

import (
	"context"
	"errors"

	controlv1 "encore.app/control/proto/controlv1"
	"encore.dev/beta/errs"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type grpcAPI struct {
	controlv1.UnimplementedModelControlServer
	controlv1.UnimplementedBackendControlServer
	svc *Service
}

func (g *grpcAPI) ListModels(ctx context.Context, _ *controlv1.ListModelsRequest) (*controlv1.ListModelsResponse, error) {
	snaps, err := g.svc.listSnapshots(ctx)
	if err != nil {
		return nil, toGRPC(err)
	}
	out := &controlv1.ListModelsResponse{Models: make([]*controlv1.Model, 0, len(snaps))}
	for i := range snaps {
		out.Models = append(out.Models, snapshotToProto(&snaps[i]))
	}
	return out, nil
}

func (g *grpcAPI) GetModel(ctx context.Context, req *controlv1.GetModelRequest) (*controlv1.Model, error) {
	snap, err := g.svc.lookupSnapshot(ctx, req.GetId())
	if err != nil {
		return nil, toGRPC(err)
	}
	return snapshotToProto(snap), nil
}

func (g *grpcAPI) LoadModel(ctx context.Context, req *controlv1.LoadModelRequest) (*controlv1.Model, error) {
	snap, err := g.svc.loadModel(ctx, req.GetId())
	if err != nil {
		return nil, toGRPC(err)
	}
	return snapshotToProto(snap), nil
}

func (g *grpcAPI) UnloadModel(ctx context.Context, req *controlv1.UnloadModelRequest) (*controlv1.Model, error) {
	snap, err := g.svc.unloadModel(ctx, req.GetId())
	if err != nil {
		return nil, toGRPC(err)
	}
	return snapshotToProto(snap), nil
}

func (g *grpcAPI) ReloadModel(ctx context.Context, req *controlv1.ReloadModelRequest) (*controlv1.Model, error) {
	snap, err := g.svc.reloadModel(ctx, req.GetId())
	if err != nil {
		return nil, toGRPC(err)
	}
	return snapshotToProto(snap), nil
}

func (g *grpcAPI) ListBackends(ctx context.Context, _ *controlv1.ListBackendsRequest) (*controlv1.ListBackendsResponse, error) {
	snaps, err := g.svc.backendSnapshots(ctx)
	if err != nil {
		return nil, toGRPC(err)
	}
	out := &controlv1.ListBackendsResponse{Backends: make([]*controlv1.Backend, 0, len(snaps))}
	for i := range snaps {
		out.Backends = append(out.Backends, backendToProto(&snaps[i]))
	}
	return out, nil
}

func (g *grpcAPI) GetBackend(ctx context.Context, req *controlv1.GetBackendRequest) (*controlv1.Backend, error) {
	snap, err := g.svc.lookupBackend(ctx, req.GetKind())
	if err != nil {
		return nil, toGRPC(err)
	}
	return backendToProto(snap), nil
}

func (g *grpcAPI) Health(ctx context.Context, _ *controlv1.HealthRequest) (*controlv1.HealthResponse, error) {
	snaps, err := g.svc.backendSnapshots(ctx)
	if err != nil {
		return nil, toGRPC(err)
	}
	out := &controlv1.HealthResponse{Backends: make([]*controlv1.Backend, 0, len(snaps))}
	for i := range snaps {
		out.Backends = append(out.Backends, backendToProto(&snaps[i]))
	}
	return out, nil
}

func snapshotToProto(s *Snapshot) *controlv1.Model {
	return &controlv1.Model{
		Id:            s.ID,
		Frontend:      s.Frontend,
		Path:          s.Path,
		Purpose:       s.Purpose,
		NativeId:      s.NativeID,
		DesiredState:  s.Desired,
		ObservedState: s.Observed,
		LastError:     s.LastError,
		SocketPath:    s.SocketPath,
		Pid:           s.PID,
		MemoryMb:      s.MemoryMB,
	}
}

func backendToProto(s *BackendSnapshot) *controlv1.Backend {
	return &controlv1.Backend{
		Kind:          s.Kind,
		SocketPath:    s.SocketPath,
		ObservedState: s.Observed,
		LastError:     s.LastError,
		Ready:         s.Ready,
		Pids:          s.PIDs,
		MemoryMb:      s.MemoryMB,
	}
}

func toGRPC(err error) error {
	if err == nil {
		return nil
	}
	var e *errs.Error
	if errors.As(err, &e) {
		switch e.Code {
		case errs.NotFound:
			return status.Error(codes.NotFound, e.Message)
		case errs.InvalidArgument:
			return status.Error(codes.InvalidArgument, e.Message)
		default:
			return status.Error(codes.Internal, e.Message)
		}
	}
	return status.Error(codes.Internal, err.Error())
}
