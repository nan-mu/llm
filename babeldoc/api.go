package babeldoc

import (
	"context"
	"fmt"

	"encore.app/internal/dochash"
	"encore.dev/beta/errs"
	"encore.dev/rlog"
)

// DocumentRef is a task id and status.
type DocumentRef struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// DocumentList is the full task list.
type DocumentList struct {
	Documents []DocumentRef `json:"documents"`
}

// UpsertSourceParams is a source PDF upload from the zotero facade.
type UpsertSourceParams struct {
	ClientSHA256 string `json:"client_sha256"`
	PDF          []byte `json:"pdf"`
}

// UpsertSourceResult tells the facade which HTTP status to use.
type UpsertSourceResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Created bool   `json:"created"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// DualFile is the bilingual PDF artifact, when one exists.
type DualFile struct {
	PDF         []byte `json:"pdf"`
	ArtifactSHA string `json:"artifact_sha256"`
	Status      string `json:"status"`
	Found       bool   `json:"found"`
	Ready       bool   `json:"ready"`
}

// DeleteResult acknowledges delete.
type DeleteResult struct {
	Ack bool `json:"ack"`
}

// RetryParams identifies the document to retry.
type RetryParams struct {
	Hash string `json:"hash"`
}

// RetryOutcome distinguishes a re-queue from an idempotent success.
type RetryOutcome struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Found    bool   `json:"found"`
	Requeued bool   `json:"requeued"`
}

// ListDocuments returns every translation task.
//
//encore:api private method=GET path=/babeldoc/documents
func ListDocuments(ctx context.Context) (*DocumentList, error) {
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

// UpsertSource stores a pending task or returns the existing one.
// It publishes babeldoc-translate and does not run a PDF worker.
//
//encore:api private method=POST path=/babeldoc/documents
func UpsertSource(ctx context.Context, p *UpsertSourceParams) (*UpsertSourceResult, error) {
	if p == nil || len(p.PDF) == 0 {
		return &UpsertSourceResult{Code: "INVALID_ARGUMENT", Message: "empty pdf"}, nil
	}
	if int64(len(p.PDF)) > MaxUploadBytes {
		return &UpsertSourceResult{Code: "PAYLOAD_TOO_LARGE", Message: fmt.Sprintf("pdf exceeds %d bytes", MaxUploadBytes)}, nil
	}
	if !dochash.LooksLikePDF(p.PDF) {
		return &UpsertSourceResult{Code: "INVALID_PDF", Message: "body is not a pdf"}, nil
	}
	computed := dochash.SourceID(p.PDF)
	if p.ClientSHA256 != "" {
		if !dochash.Valid(p.ClientSHA256) {
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
		existing, err2 := getMeta(ctx, computed)
		if err2 == nil && existing != nil {
			return &UpsertSourceResult{ID: existing.ID, Status: existing.Status, Created: false}, nil
		}
		return nil, errs.WrapCode(err, errs.Internal, "insert document")
	}
	if _, err := TranslateTopic.Publish(ctx, &TranslateEvent{Hash: computed}); err != nil {
		rlog.Error("babeldoc publish failed after insert", "err", err)
	}
	return &UpsertSourceResult{ID: computed, Status: statusPending, Created: true}, nil
}

// GetDual returns the bilingual PDF when status is down.
//
//encore:api private method=GET path=/babeldoc/documents/:hash/dual
func GetDual(ctx context.Context, hash string) (*DualFile, error) {
	if !dochash.Valid(hash) {
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

// DeleteDocument removes a task. It is idempotent.
//
//encore:api private method=DELETE path=/babeldoc/documents/:hash
func DeleteDocument(ctx context.Context, hash string) (*DeleteResult, error) {
	if !dochash.Valid(hash) {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "invalid hash"}
	}
	if err := deleteDocument(ctx, hash); err != nil {
		return nil, errs.WrapCode(err, errs.Internal, "delete document")
	}
	return &DeleteResult{Ack: true}, nil
}

// Retry re-queues an error task. pending and down stay as they are.
// A re-queue publishes babeldoc-translate and does not run a PDF worker.
//
//encore:api private method=POST path=/babeldoc/documents/retry
func Retry(ctx context.Context, p *RetryParams) (*RetryOutcome, error) {
	if p == nil || !dochash.Valid(p.Hash) {
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
			rlog.Error("babeldoc retry publish failed", "err", err)
		}
	}
	return &RetryOutcome{ID: p.Hash, Status: status, Found: true, Requeued: requeued}, nil
}
