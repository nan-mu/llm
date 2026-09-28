package babeldoc

import (
	"context"
	"errors"

	"encore.dev/storage/sqldb"
)

type documentRow struct {
	ID           string
	Status       string
	SourcePDF    []byte
	DualPDF      []byte
	DualSHA256   string
	ErrorCode    string
	ErrorMessage string
	Generation   int64
}

func insertPending(ctx context.Context, id string, source []byte) error {
	_, err := db.Exec(ctx, `
		INSERT INTO documents (id, status, source_pdf)
		VALUES ($1, $2, $3)
	`, id, statusPending, source)
	return err
}

func getMeta(ctx context.Context, id string) (*documentRow, error) {
	var row documentRow
	err := db.QueryRow(ctx, `
		SELECT id, status, COALESCE(dual_sha256, ''), error_code, error_message, generation
		FROM documents WHERE id = $1
	`, id).Scan(&row.ID, &row.Status, &row.DualSHA256, &row.ErrorCode, &row.ErrorMessage, &row.Generation)
	if errors.Is(err, sqldb.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func getSourcePDF(ctx context.Context, id string) ([]byte, error) {
	var pdf []byte
	err := db.QueryRow(ctx, `SELECT source_pdf FROM documents WHERE id = $1`, id).Scan(&pdf)
	if errors.Is(err, sqldb.ErrNoRows) {
		return nil, nil
	}
	return pdf, err
}

func getDualPDF(ctx context.Context, id string) (pdf []byte, dualSHA string, status string, err error) {
	err = db.QueryRow(ctx, `
		SELECT status, dual_pdf, COALESCE(dual_sha256, '')
		FROM documents WHERE id = $1
	`, id).Scan(&status, &pdf, &dualSHA)
	if errors.Is(err, sqldb.ErrNoRows) {
		return nil, "", "", nil
	}
	return pdf, dualSHA, status, err
}

func listAllMeta(ctx context.Context) ([]documentRow, error) {
	rows, err := db.Query(ctx, `
		SELECT id, status FROM documents ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []documentRow
	for rows.Next() {
		var r documentRow
		if err := rows.Scan(&r.ID, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func listPendingIDs(ctx context.Context) ([]string, error) {
	rows, err := db.Query(ctx, `
		SELECT id FROM documents WHERE status = $1 ORDER BY created_at ASC
	`, statusPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// claimPending marks a pending row as started. Returns false if not claimable.
func claimPending(ctx context.Context, id string) (bool, error) {
	res, err := db.Exec(ctx, `
		UPDATE documents
		SET started_at = NOW(), updated_at = NOW(),
		    error_code = '', error_message = ''
		WHERE id = $1 AND status = $2
	`, id, statusPending)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() == 1, nil
}

func markDown(ctx context.Context, id string, dual []byte, dualSHA string) error {
	_, err := db.Exec(ctx, `
		UPDATE documents
		SET status = $2, dual_pdf = $3, dual_sha256 = $4,
		    error_code = '', error_message = '',
		    finished_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND status = $5
	`, id, statusDown, dual, dualSHA, statusPending)
	return err
}

func markError(ctx context.Context, id, code, message string) error {
	_, err := db.Exec(ctx, `
		UPDATE documents
		SET status = $2, error_code = $3, error_message = $4,
		    finished_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND status = $5
	`, id, statusError, code, message, statusPending)
	return err
}

func resetForRetry(ctx context.Context, id string) (string, bool, error) {
	row, err := getMeta(ctx, id)
	if err != nil {
		return "", false, err
	}
	if row == nil {
		return "", false, nil
	}
	if row.Status == statusPending || row.Status == statusDown {
		return row.Status, true, nil
	}
	if row.Status != statusError {
		return row.Status, true, nil
	}
	_, err = db.Exec(ctx, `
		UPDATE documents
		SET status = $2, dual_pdf = NULL, dual_sha256 = NULL,
		    error_code = '', error_message = '',
		    started_at = NULL, finished_at = NULL,
		    generation = generation + 1,
		    updated_at = NOW()
		WHERE id = $1 AND status = $3
	`, id, statusPending, statusError)
	if err != nil {
		return "", false, err
	}
	return statusPending, true, nil
}

func deleteDocument(ctx context.Context, id string) error {
	_, err := db.Exec(ctx, `DELETE FROM documents WHERE id = $1`, id)
	return err
}