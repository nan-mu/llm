package babeldoc

import (
	"context"
	"fmt"

	"encore.dev/beta/errs"
	"encore.dev/rlog"
)

// DocumentRef is id + status for list/upsert/retry responses.
type DocumentRef struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// DocumentList is the full task list (no pagination).
type DocumentList struct {
	Documents []DocumentRef `json:"documents"`
}

// UpsertSourceParams is a source PDF upload.
type UpsertSourceParams struct {
	ClientSHA256 string `json:"client_sha256"`
	PDF          []byte `json:"pdf"`
}

// UpsertSourceResult includes HTTP-oriented outcome for the zotero facade.
type UpsertSourceResult struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Created   bool   `json:"created"` // true → caller should respond 202
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

// DualFile is the bilingual PDF artifact.
type DualFile struct {
	PDF          []byte `json:"pdf"`
	ArtifactSHA  string `json:"artifact_sha256"`
	Status       string `json:"status"`
	Found        bool   `json:"found"`
	Ready        bool   `json:"ready"`
}

// DeleteResult acknowledges delete (always success semantically).
type DeleteResult struct {
	Ack bool `json:"ack"`
}

// ListDocuments returns all translation tasks.
//
//encore:api private method=GET path=/babeldoc/documents
func (s *Service) ListDocuments(ctx context.Context) (*DocumentList, error) {
	rows, err := listAllMeta(ctx)
	if err != nil {
		return nil, errs.WrapCode(err, errs.Internal, "list documents")
	}
	out := &DocumentList{Documents: make([]DocumentRef, 0, len(rows))}
	for _, r := range rows {
		out.Documents = append(out.Documents, DocumentRef{ID: r.ID, Status: r.Status})
	}
	return out, nil
}

// UpsertSource stores a new pending task or returns the existing status.
//
//encore:api private method=POST path=/babeldoc/documents
func (s *Service) UpsertSource(ctx context.Context, p *UpsertSourceParams) (*UpsertSourceResult, error) {
	if p == nil || len(p.PDF) == 0 {
		return &UpsertSourceResult{Code: "INVALID_ARGUMENT", Message: "empty pdf"}, nil
	}
	if int64(len(p.PDF)) > s.maxUploadBytes {
		return &UpsertSourceResult{Code: "PAYLOAD_TOO_LARGE", Message: fmt.Sprintf("pdf exceeds %d bytes", s.maxUploadBytes)}, nil
	}
	if !looksLikePDF(p.PDF) {
		return &UpsertSourceResult{Code: "INVALID_PDF", Message: "body is not a pdf"}, nil
	}
	computed := sha256Bytes(p.PDF)
	if p.ClientSHA256 != "" {
		if !validHash(p.ClientSHA256) {
			return &UpsertSourceResult{Code: "INVALID_ARGUMENT", Message: "X-Document-SHA256 must be 64 lowercase hex"}, nil
		}
		if p.ClientSHA256 != computed {
			return &UpsertSourceResult{Code: "HASH_MISMATCH", Message: "computed sha256 does not match header"}, nil
		}
	}

	existing, err := getMeta(ctx, computed)
	if err != nil {
		return nil, errs.WrapCode(err, errs.Internal, "lookup document")
	}
	if existing != nil {
		return &UpsertSourceResult{ID: existing.ID, Status: existing.Status, Created: false}, nil
	}

	if err := insertPending(ctx, computed, p.PDF); err != nil {
		// Race: another insert won.
		existing, err2 := getMeta(ctx, computed)
		if err2 == nil && existing != nil {
			return &UpsertSourceResult{ID: existing.ID, Status: existing.Status, Created: false}, nil
		}
		return nil, errs.WrapCode(err, errs.Internal, "insert document")
	}

	if _, err := TranslateTopic.Publish(ctx, &TranslateEvent{Hash: computed}); err != nil {
		rlog.Error("babeldoc publish failed after insert", "event", "babeldoc.publish_failed",
			"hash_prefix", shortHash(computed), "err", err)
		// Task is pending; requeue on restart will pick it up.
	}
	return &UpsertSourceResult{ID: computed, Status: statusPending, Created: true}, nil
}

// GetDual returns the bilingual PDF when status is down.
//
//encore:api private method=GET path=/babeldoc/documents/:hash/dual
func (s *Service) GetDual(ctx context.Context, hash string) (*DualFile, error) {
	if !validHash(hash) {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "invalid hash"}
	}
	pdf, dualSHA, status, err := getDualPDF(ctx, hash)
	if err != nil {
		return nil, errs.WrapCode(err, errs.Internal, "get dual")
	}
	if status == "" {
		return &DualFile{Found: false}, nil
	}
	if status != statusDown || len(pdf) == 0 {
		return &DualFile{Found: true, Ready: false, Status: status}, nil
	}
	return &DualFile{
		Found:       true,
		Ready:       true,
		Status:      status,
		PDF:         pdf,
		ArtifactSHA: dualSHA,
	}, nil
}

// DeleteDocument removes a task (idempotent).
//
//encore:api private method=DELETE path=/babeldoc/documents/:hash
func (s *Service) DeleteDocument(ctx context.Context, hash string) (*DeleteResult, error) {
	if !validHash(hash) {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "invalid hash"}
	}
	if err := deleteDocument(ctx, hash); err != nil {
		return nil, errs.WrapCode(err, errs.Internal, "delete document")
	}
	return &DeleteResult{Ack: true}, nil
}

// RetryOutcome helps the HTTP layer pick 202 vs 200.
type RetryOutcome struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Found    bool   `json:"found"`
	Requeued bool   `json:"requeued"` // true when transitioning from error → pending
}

// RetryParams identifies the document to retry.
type RetryParams struct {
	Hash string `json:"hash"`
}

// Retry re-queues an error task; pending/down are idempotent success.
//
//encore:api private method=POST path=/babeldoc/documents/retry
func (s *Service) Retry(ctx context.Context, p *RetryParams) (*RetryOutcome, error) {
	if p == nil || !validHash(p.Hash) {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "invalid hash"}
	}
	before, err := getMeta(ctx, p.Hash)
	if err != nil {
		return nil, errs.WrapCode(err, errs.Internal, "retry lookup")
	}
	if before == nil {
		return &RetryOutcome{Found: false}, nil
	}
	wasError := before.Status == statusError
	status, ok, err := resetForRetry(ctx, p.Hash)
	if err != nil {
		return nil, errs.WrapCode(err, errs.Internal, "retry document")
	}
	if !ok {
		return &RetryOutcome{Found: false}, nil
	}
	requeued := wasError && status == statusPending
	if requeued {
		if _, err := TranslateTopic.Publish(ctx, &TranslateEvent{Hash: p.Hash}); err != nil {
			rlog.Error("babeldoc retry publish failed", "event", "babeldoc.retry_publish_failed",
				"hash_prefix", shortHash(p.Hash), "err", err)
		}
	}
	return &RetryOutcome{ID: p.Hash, Status: status, Found: true, Requeued: requeued}, nil
}
