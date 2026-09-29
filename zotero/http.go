// Package zotero is the public Zotero-plugin HTTP contract.
// There is no auth in this slice. Persistence and translation tasks are delegated to babeldoc.
package zotero

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"encore.app/babeldoc"
	"encore.app/internal/dochash"
	"encore.dev"
	"encore.dev/beta/errs"
	"encore.dev/rlog"
)

// HealthResponse is the facade health body.
type HealthResponse struct {
	OK bool `json:"ok"`
}

// Health reports that the zotero facade is up.
//
//encore:api public method=GET path=/v1/health
func Health(ctx context.Context) (*HealthResponse, error) {
	return &HealthResponse{OK: true}, nil
}

// ListDocumentsResponse is GET /v1/documents.
type ListDocumentsResponse struct {
	Documents []babeldoc.DocumentRef `json:"documents"`
}

// ListDocuments returns the full task list.
//
//encore:api public method=GET path=/v1/documents
func ListDocuments(ctx context.Context) (*ListDocumentsResponse, error) {
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

// DeleteDocument removes a task. Missing tasks still acknowledge.
//
//encore:api public method=DELETE path=/v1/documents/:hash
func DeleteDocument(ctx context.Context, hash string) (*DeleteResponse, error) {
	if !dochash.Valid(hash) {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "invalid hash"}
	}
	res, err := babeldoc.DeleteDocument(ctx, hash)
	if err != nil {
		return nil, err
	}
	return &DeleteResponse{Ack: res.Ack}, nil
}

// SubmitDocument accepts a raw PDF body.
//
//encore:api public raw method=POST path=/v1/documents
func SubmitDocument(w http.ResponseWriter, req *http.Request) {
	ct := req.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/pdf") {
		writeErr(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/pdf")
		return
	}
	clientHash := strings.TrimSpace(req.Header.Get("X-Document-SHA256"))
	body, err := io.ReadAll(io.LimitReader(req.Body, babeldoc.MaxUploadBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read body")
		return
	}
	res, err := babeldoc.UpsertSource(req.Context(), &babeldoc.UpsertSourceParams{
		ClientSHA256: clientHash,
		PDF:          body,
	})
	if err != nil {
		rlog.Error("zotero submit failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "submit failed")
		return
	}
	switch res.Code {
	case "HASH_MISMATCH":
		writeErr(w, http.StatusUnprocessableEntity, "HASH_MISMATCH", res.Message)
		return
	case "PAYLOAD_TOO_LARGE":
		writeErr(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", res.Message)
		return
	case "INVALID_PDF", "INVALID_ARGUMENT":
		writeErr(w, http.StatusBadRequest, res.Code, res.Message)
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, map[string]string{"id": res.ID, "status": res.Status})
}

// DownloadDual returns the bilingual PDF when the task is down.
//
//encore:api public raw method=GET path=/v1/documents/:hash/files/dual
func DownloadDual(w http.ResponseWriter, req *http.Request) {
	hash := encore.CurrentRequest().PathParams.Get("hash")
	if !dochash.Valid(hash) {
		writeErr(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid hash")
		return
	}
	file, err := babeldoc.GetDual(req.Context(), hash)
	if err != nil {
		rlog.Error("zotero download failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "download failed")
		return
	}
	if !file.Found {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "document not found")
		return
	}
	if !file.Ready {
		writeErr(w, http.StatusConflict, "NOT_READY", "document is not ready for download")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", strconv.Itoa(len(file.PDF)))
	w.Header().Set("X-Artifact-SHA256", file.ArtifactSHA)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.PDF)
}

// RetryDocument re-queues an error task. It does not start a PDF worker.
//
//encore:api public raw method=POST path=/v1/documents/:hash/retry
func RetryDocument(w http.ResponseWriter, req *http.Request) {
	hash := encore.CurrentRequest().PathParams.Get("hash")
	if !dochash.Valid(hash) {
		writeErr(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid hash")
		return
	}
	out, err := babeldoc.Retry(req.Context(), &babeldoc.RetryParams{Hash: hash})
	if err != nil {
		rlog.Error("zotero retry failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "retry failed")
		return
	}
	if !out.Found {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "document not found")
		return
	}
	status := http.StatusOK
	if out.Requeued {
		status = http.StatusAccepted
	}
	writeJSON(w, status, map[string]string{"id": out.ID, "status": out.Status})
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	var body apiError
	body.Error.Code = code
	body.Error.Message = message
	writeJSON(w, status, body)
}
