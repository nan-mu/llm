package zotero

import (
	"context"

	"encore.app/babeldoc"
	"encore.dev/beta/errs"
)

// HealthResponse is the optional health payload.
type HealthResponse struct {
	OK bool `json:"ok"`
}

// Health checks that the zotero facade is up (auth is no-op this slice).
//
//encore:api public method=GET path=/v1/health
func (s *Service) Health(ctx context.Context) (*HealthResponse, error) {
	return &HealthResponse{OK: true}, nil
}

// ListDocumentsResponse matches GET /v1/documents.
type ListDocumentsResponse struct {
	Documents []babeldoc.DocumentRef `json:"documents"`
}

// ListDocuments returns the full task list.
//
//encore:api public method=GET path=/v1/documents
func (s *Service) ListDocuments(ctx context.Context) (*ListDocumentsResponse, error) {
	list, err := babeldoc.ListDocuments(ctx)
	if err != nil {
		return nil, err
	}
	return &ListDocumentsResponse{Documents: list.Documents}, nil
}

// DeleteResponse is DELETE /v1/documents/:hash.
type DeleteResponse struct {
	Ack bool `json:"ack"`
}

// DeleteDocument removes a server-side task (idempotent).
//
//encore:api public method=DELETE path=/v1/documents/:hash
func (s *Service) DeleteDocument(ctx context.Context, hash string) (*DeleteResponse, error) {
	if !validHash(hash) {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "invalid hash"}
	}
	res, err := babeldoc.DeleteDocument(ctx, hash)
	if err != nil {
		return nil, err
	}
	return &DeleteResponse{Ack: res.Ack}, nil
}
