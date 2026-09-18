package control

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"encore.app/internal/modelstate"
	"encore.dev/beta/errs"
	"encore.dev/storage/sqldb"
)

type modelRow struct {
	ID                string
	Frontend          modelstate.FrontendKind
	Path              string
	Purpose           modelstate.Purpose
	NativeID          string
	Desired           modelstate.ModelState
	Observed          modelstate.ModelState
	LastError         string
	SocketPath        *string // nil for mlxlm
	PID               *int64  // nil when unloaded
	RestartPolicy     string
	RestartMaxRetries *int
}

type frontendRow struct {
	Kind       modelstate.FrontendKind
	SocketPath *string
	Observed   modelstate.FrontendState
	LastError  string
	PIDs       []int64
	MemoryMB   *int64
}

func getModel(ctx context.Context, id string) (*modelRow, error) {
	var row modelRow
	var lastErr sql.NullString
	var sock sql.NullString
	var pid sql.NullInt64
	var maxRetries sql.NullInt64
	err := db.QueryRow(ctx, `
		SELECT m.id, m.frontend, m.path, m.purpose, m.native_id,
		       m.desired_state, m.observed_state, m.last_error, f.socket_path, m.pid,
		       m.restart_policy, m.restart_max_retries
		FROM models m
		JOIN frontends f ON f.kind = m.frontend
		WHERE m.id = $1
	`, id).Scan(
		&row.ID, &row.Frontend, &row.Path, &row.Purpose, &row.NativeID,
		&row.Desired, &row.Observed, &lastErr, &sock, &pid,
		&row.RestartPolicy, &maxRetries,
	)
	if errors.Is(err, sqldb.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: "model not found"}
	}
	if err != nil {
		return nil, err
	}
	row.LastError = lastErr.String
	row.SocketPath = nullStringPtr(sock)
	row.PID = nullInt64Ptr(pid)
	row.RestartMaxRetries = nullIntPtr(maxRetries)
	if row.RestartPolicy == "" {
		row.RestartPolicy = restartPolicyUnlessStopped
	}
	return &row, nil
}

// getModelByFrontendRef resolves a catalog row by catalog id or native_id on kind.
func getModelByFrontendRef(ctx context.Context, kind modelstate.FrontendKind, ref string) (*modelRow, error) {
	var row modelRow
	var lastErr sql.NullString
	var sock sql.NullString
	var pid sql.NullInt64
	var maxRetries sql.NullInt64
	err := db.QueryRow(ctx, `
		SELECT m.id, m.frontend, m.path, m.purpose, m.native_id,
		       m.desired_state, m.observed_state, m.last_error, f.socket_path, m.pid,
		       m.restart_policy, m.restart_max_retries
		FROM models m
		JOIN frontends f ON f.kind = m.frontend
		WHERE m.frontend = $1 AND (m.id = $2 OR m.native_id = $2)
		ORDER BY CASE WHEN m.id = $2 THEN 0 ELSE 1 END
		LIMIT 1
	`, kind, ref).Scan(
		&row.ID, &row.Frontend, &row.Path, &row.Purpose, &row.NativeID,
		&row.Desired, &row.Observed, &lastErr, &sock, &pid,
		&row.RestartPolicy, &maxRetries,
	)
	if errors.Is(err, sqldb.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: "model not found"}
	}
	if err != nil {
		return nil, err
	}
	row.LastError = lastErr.String
	row.SocketPath = nullStringPtr(sock)
	row.PID = nullInt64Ptr(pid)
	row.RestartMaxRetries = nullIntPtr(maxRetries)
	if row.RestartPolicy == "" {
		row.RestartPolicy = restartPolicyUnlessStopped
	}
	return &row, nil
}

func listModels(ctx context.Context) ([]modelRow, error) {
	rows, err := db.Query(ctx, `
		SELECT m.id, m.frontend, m.path, m.purpose, m.native_id,
		       m.desired_state, m.observed_state, m.last_error, f.socket_path, m.pid,
		       m.restart_policy, m.restart_max_retries
		FROM models m
		JOIN frontends f ON f.kind = m.frontend
		ORDER BY m.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanModels(rows)
}

func listDesiredLoaded(ctx context.Context) ([]modelRow, error) {
	rows, err := db.Query(ctx, `
		SELECT m.id, m.frontend, m.path, m.purpose, m.native_id,
		       m.desired_state, m.observed_state, m.last_error, f.socket_path, m.pid,
		       m.restart_policy, m.restart_max_retries
		FROM models m
		JOIN frontends f ON f.kind = m.frontend
		WHERE m.desired_state = 'loaded'
		ORDER BY m.frontend, m.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanModels(rows)
}

func scanModels(rows *sqldb.Rows) ([]modelRow, error) {
	var out []modelRow
	for rows.Next() {
		var row modelRow
		var lastErr sql.NullString
		var sock sql.NullString
		var pid sql.NullInt64
		var maxRetries sql.NullInt64
		if err := rows.Scan(
			&row.ID, &row.Frontend, &row.Path, &row.Purpose, &row.NativeID,
			&row.Desired, &row.Observed, &lastErr, &sock, &pid,
			&row.RestartPolicy, &maxRetries,
		); err != nil {
			return nil, err
		}
		row.LastError = lastErr.String
		row.SocketPath = nullStringPtr(sock)
		row.PID = nullInt64Ptr(pid)
		row.RestartMaxRetries = nullIntPtr(maxRetries)
		if row.RestartPolicy == "" {
			row.RestartPolicy = restartPolicyUnlessStopped
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func listFrontends(ctx context.Context) ([]frontendRow, error) {
	rows, err := db.Query(ctx, `
		SELECT kind, socket_path, observed_state, last_error, pids::text, memory_mb
		FROM frontends
		ORDER BY kind
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []frontendRow
	for rows.Next() {
		row, err := scanFrontend(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func getFrontend(ctx context.Context, kind modelstate.FrontendKind) (*frontendRow, error) {
	row := db.QueryRow(ctx, `
		SELECT kind, socket_path, observed_state, last_error, pids::text, memory_mb
		FROM frontends
		WHERE kind = $1
	`, kind)
	out, err := scanFrontendRow(row)
	if errors.Is(err, sqldb.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: "frontend not found"}
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanFrontend(rows *sqldb.Rows) (frontendRow, error) {
	return scanFrontendRow(rows)
}

func scanFrontendRow(s scannable) (frontendRow, error) {
	var row frontendRow
	var lastErr sql.NullString
	var sock sql.NullString
	var pidsText sql.NullString
	var mem sql.NullInt64
	if err := s.Scan(&row.Kind, &sock, &row.Observed, &lastErr, &pidsText, &mem); err != nil {
		return row, err
	}
	row.LastError = lastErr.String
	row.SocketPath = nullStringPtr(sock)
	row.PIDs = parseInt64Array(pidsText.String)
	row.MemoryMB = nullInt64Ptr(mem)
	return row, nil
}

func resetObserved(ctx context.Context) error {
	if _, err := db.Exec(ctx, `
		UPDATE models
		SET observed_state = 'unloaded', last_error = NULL, pid = NULL, updated_at = NOW()
	`); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `
		UPDATE frontends
		SET observed_state = 'stopped', last_error = NULL, pids = NULL, memory_mb = NULL, updated_at = NOW()
	`)
	return err
}

func setModelDesired(ctx context.Context, id string, desired modelstate.ModelState) error {
	_, err := db.Exec(ctx, `
		UPDATE models
		SET desired_state = $2, updated_at = NOW()
		WHERE id = $1
	`, id, desired)
	return err
}

func setModelObserved(ctx context.Context, id string, observed modelstate.ModelState, lastErr string) error {
	_, err := db.Exec(ctx, `
		UPDATE models
		SET observed_state = $2, last_error = NULLIF($3, ''), updated_at = NOW()
		WHERE id = $1
	`, id, observed, lastErr)
	return err
}

func setModelPID(ctx context.Context, id string, pid *int64) error {
	_, err := db.Exec(ctx, `
		UPDATE models
		SET pid = $2, updated_at = NOW()
		WHERE id = $1
	`, id, pid)
	return err
}

func setFrontendObserved(ctx context.Context, kind modelstate.FrontendKind, observed modelstate.FrontendState, lastErr string) error {
	_, err := db.Exec(ctx, `
		UPDATE frontends
		SET observed_state = $2, last_error = NULLIF($3, ''), updated_at = NOW()
		WHERE kind = $1
	`, kind, observed, lastErr)
	return err
}

func setFrontendMemoryMB(ctx context.Context, kind modelstate.FrontendKind, mb *int64) error {
	_, err := db.Exec(ctx, `
		UPDATE frontends
		SET memory_mb = $2, updated_at = NOW()
		WHERE kind = $1
	`, kind, mb)
	return err
}

func refreshFrontendPIDs(ctx context.Context, kind modelstate.FrontendKind) error {
	_, err := db.Exec(ctx, `
		UPDATE frontends f
		SET pids = sub.pids, updated_at = NOW()
		FROM (
			SELECT COALESCE(array_agg(m.pid ORDER BY m.id), '{}'::bigint[]) AS pids
			FROM models m
			WHERE m.frontend = $1
			  AND m.observed_state = 'loaded'
			  AND m.pid IS NOT NULL
		) sub
		WHERE f.kind = $1
	`, kind)
	return err
}

func clearFrontendRuntime(ctx context.Context, kind modelstate.FrontendKind) error {
	_, err := db.Exec(ctx, `
		UPDATE frontends
		SET pids = NULL, memory_mb = NULL, updated_at = NOW()
		WHERE kind = $1
	`, kind)
	return err
}

func insertMemorySample(ctx context.Context, kind modelstate.FrontendKind, mb int64, keep int) error {
	if _, err := db.Exec(ctx, `
		INSERT INTO frontend_memory_samples (kind, memory_mb)
		VALUES ($1, $2)
	`, kind, mb); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `
		DELETE FROM frontend_memory_samples
		WHERE kind = $1
		  AND id NOT IN (
			SELECT id FROM frontend_memory_samples
			WHERE kind = $1
			ORDER BY sampled_at DESC, id DESC
			LIMIT $2
		  )
	`, kind, keep)
	return err
}

func countActiveOnFrontend(ctx context.Context, kind modelstate.FrontendKind) (int, error) {
	var n int
	err := db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM models
		WHERE frontend = $1
		  AND (desired_state = 'loaded' OR observed_state IN ('loaded', 'loading'))
	`, kind).Scan(&n)
	return n, err
}

func insertModel(ctx context.Context, id, path, purpose string, desired modelstate.ModelState) error {
	id = strings.TrimSpace(id)
	path = strings.TrimSpace(path)
	purpose = strings.TrimSpace(purpose)
	if id == "" || path == "" {
		return &errs.Error{Code: errs.InvalidArgument, Message: "id and path are required"}
	}
	p := modelstate.Purpose(purpose)
	if !modelstate.ValidPurpose(p) {
		return &errs.Error{Code: errs.InvalidArgument, Message: "invalid purpose"}
	}
	if desired == "" {
		desired = modelstate.ModelUnloaded
	}
	if desired != modelstate.ModelUnloaded && desired != modelstate.ModelLoaded {
		return &errs.Error{Code: errs.InvalidArgument, Message: "invalid desired_state"}
	}
	frontend := modelstate.FrontendFromPath(path)
	nativeID := modelstate.NativeID(id, path)
	_, err := db.Exec(ctx, `
		INSERT INTO models (id, frontend, path, purpose, native_id, desired_state, observed_state)
		VALUES ($1, $2, $3, $4, $5, $6, 'unloaded')
	`, id, frontend, path, p, nativeID, desired)
	return err
}

func nullStringPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.String
	return &s
}

func nullInt64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func nullIntPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func parseInt64Array(s string) []int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" {
		return nil
	}
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "NULL" {
			continue
		}
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}
