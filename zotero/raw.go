package zotero

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"encore.app/babeldoc"
	"encore.dev"
	"encore.dev/rlog"
)

const maxReadBytes = 100 << 20 // hard cap aligned with babeldoc default

// SubmitDocument accepts a raw PDF body (POST /v1/documents).
//
//encore:api public raw method=POST path=/v1/documents
func (s *Service) SubmitDocument(w http.ResponseWriter, req *http.Request) {
	ct := req.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/pdf") {
		writeErr(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/pdf")
		return
	}
	clientHash := strings.TrimSpace(req.Header.Get("X-Document-SHA256"))
	body, err := io.ReadAll(io.LimitReader(req.Body, maxReadBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read body")
		return
	}
	if int64(len(body)) > maxReadBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "pdf exceeds size limit")
		return
	}

	res, err := babeldoc.UpsertSource(req.Context(), &babeldoc.UpsertSourceParams{
		ClientSHA256: clientHash,
		PDF:          body,
	})
	if err != nil {
		rlog.Error("zotero submit failed", "event", "zotero.submit_failed", "err", err)
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

// DownloadDual returns the bilingual PDF (GET /v1/documents/:hash/files/dual).
//
//encore:api public raw method=GET path=/v1/documents/:hash/files/dual
func (s *Service) DownloadDual(w http.ResponseWriter, req *http.Request) {
	hash := encore.CurrentRequest().PathParams.Get("hash")
	if !validHash(hash) {
		writeErr(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid hash")
		return
	}
	file, err := babeldoc.GetDual(req.Context(), hash)
	if err != nil {
		rlog.Error("zotero download failed", "event", "zotero.download_failed", "err", err)
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

// RetryDocument re-queues an error task (POST /v1/documents/:hash/retry).
//
//encore:api public raw method=POST path=/v1/documents/:hash/retry
func (s *Service) RetryDocument(w http.ResponseWriter, req *http.Request) {
	hash := encore.CurrentRequest().PathParams.Get("hash")
	if !validHash(hash) {
		writeErr(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid hash")
		return
	}
	out, err := babeldoc.Retry(req.Context(), &babeldoc.RetryParams{Hash: hash})
	if err != nil {
		rlog.Error("zotero retry failed", "event", "zotero.retry_failed", "err", err)
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
