package babeldoc

import (
	"context"
	"database/sql"
	"errors"

	"encore.dev/storage/sqldb"
)

type docMeta struct {
	ID     string
	Status string
}

func listAllMeta(ctx context.Context) ([]docMeta, error) {
	rows, err := db.Query(ctx, `SELECT id, status FROM documents ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []docMeta{}
	for rows.Next() {
		var m docMeta
		if err := rows.Scan(&m.ID, &m.Status); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func getMeta(ctx context.Context, id string) (*docMeta, error) {
	var m docMeta
	err := db.QueryRow(ctx, `SELECT id, status FROM documents WHERE id = $1`, id).Scan(&m.ID, &m.Status)
	if errors.Is(err, sqldb.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func insertPending(ctx context.Context, id string, pdf []byte) error {
	_, err := db.Exec(ctx, `
		INSERT INTO documents (id, status, source_pdf)
		VALUES ($1, $2, $3)
	`, id, statusPending, pdf)
	return err
}

func getDualPDF(ctx context.Context, id string) (pdf []byte, sha, status string, err error) {
	var dual []byte
	var dualSHA sql.NullString
	err = db.QueryRow(ctx, `
		SELECT status, dual_pdf, dual_sha256 FROM documents WHERE id = $1
	`, id).Scan(&status, &dual, &dualSHA)
	if errors.Is(err, sqldb.ErrNoRows) {
		return nil, "", "", nil
	}
	if err != nil {
		return nil, "", "", err
	}
	if dualSHA.Valid {
		sha = dualSHA.String
	}
	return dual, sha, status, nil
}

func deleteDocument(ctx context.Context, id string) error {
	_, err := db.Exec(ctx, `DELETE FROM documents WHERE id = $1`, id)
	return err
}

// resetForRetry moves an error task back to pending. Other statuses are left unchanged.
func resetForRetry(ctx context.Context, id string) (status string, ok bool, err error) {
	err = db.QueryRow(ctx, `
		UPDATE documents
		SET status = $2, error_code = '', error_message = '', generation = generation + 1, updated_at = NOW()
		WHERE id = $1 AND status = $3
		RETURNING status
	`, id, statusPending, statusError).Scan(&status)
	if err == nil {
		return status, true, nil
	}
	if !errors.Is(err, sqldb.ErrNoRows) {
		return "", false, err
	}
	meta, err := getMeta(ctx, id)
	if err != nil {
		return "", false, err
	}
	if meta == nil {
		return "", false, nil
	}
	return meta.Status, true, nil
}
