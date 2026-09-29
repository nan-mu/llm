package control

import (
	"context"
	"database/sql"
	"errors"

	"encore.app/internal/modelstate"
	"encore.dev/beta/errs"
	"encore.dev/storage/sqldb"
)

// Snapshot is the catalog view of one model. It does not include secrets or sockets contents.
type Snapshot struct {
	ID                string  `json:"id"`
	Frontend          string  `json:"frontend"`
	Path              string  `json:"path"`
	Purpose           string  `json:"purpose"`
	NativeID          string  `json:"native_id"`
	Desired           string  `json:"desired_state"`
	Observed          string  `json:"observed_state"`
	SocketPath        *string `json:"socket_path,omitempty"`
	LastError         string  `json:"last_error,omitempty"`
	PID               *int64  `json:"pid,omitempty"`
	MemoryMB          *int64  `json:"memory_mb,omitempty"`
	RestartPolicy     string  `json:"restart_policy"`
	RestartMaxRetries *int    `json:"restart_max_retries,omitempty"`
}

// GetSnapshot returns one catalog row. It does not start a frontend.
//
//encore:api private method=GET path=/control/models/:id
func (s *Service) GetSnapshot(ctx context.Context, id string) (*Snapshot, error) {
	row, err := getModel(ctx, id)
	if err != nil {
		return nil, err
	}
	snap := row.snapshot()
	return &snap, nil
}

// ListSnapshotsResponse is the private catalog listing.
type ListSnapshotsResponse struct {
	Models []Snapshot `json:"models"`
}

// ListSnapshots returns catalog rows. It does not start a frontend.
//
//encore:api private method=GET path=/control/models
func (s *Service) ListSnapshots(ctx context.Context) (*ListSnapshotsResponse, error) {
	rows, err := listModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Snapshot, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].snapshot())
	}
	return &ListSnapshotsResponse{Models: out}, nil
}

type modelRow struct {
	ID                string
	Frontend          modelstate.FrontendKind
	Path              string
	Purpose           modelstate.Purpose
	NativeID          string
	Desired           modelstate.ModelState
	Observed          modelstate.ModelState
	LastError         string
	SocketPath        *string
	PID               *int64
	MemoryMB          *int64
	RestartPolicy     string
	RestartMaxRetries *int
}

func (row modelRow) snapshot() Snapshot {
	return Snapshot{
		ID:                row.ID,
		Frontend:          string(row.Frontend),
		Path:              row.Path,
		Purpose:           string(row.Purpose),
		NativeID:          row.NativeID,
		Desired:           string(row.Desired),
		Observed:          string(row.Observed),
		SocketPath:        row.SocketPath,
		LastError:         row.LastError,
		PID:               row.PID,
		MemoryMB:          row.MemoryMB,
		RestartPolicy:     row.RestartPolicy,
		RestartMaxRetries: row.RestartMaxRetries,
	}
}

const modelSelect = `
	SELECT m.id, m.frontend, m.path, m.purpose, m.native_id,
	       m.desired_state, m.observed_state, COALESCE(m.last_error, ''),
	       f.socket_path, m.pid, f.memory_mb,
	       m.restart_policy, m.restart_max_retries
	FROM models m
	JOIN frontends f ON f.kind = m.frontend
`

func getModel(ctx context.Context, id string) (*modelRow, error) {
	row := db.QueryRow(ctx, modelSelect+` WHERE m.id = $1`, id)
	out, err := scanModel(row.Scan)
	if errors.Is(err, sqldb.ErrNoRows) {
		return nil, &errs.Error{Code: errs.NotFound, Message: "model not found"}
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func listModels(ctx context.Context) ([]modelRow, error) {
	rows, err := db.Query(ctx, modelSelect+` ORDER BY m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanModelRows(rows)
}

func listDesiredLoaded(ctx context.Context) ([]modelRow, error) {
	rows, err := db.Query(ctx, modelSelect+` WHERE m.desired_state = 'loaded' ORDER BY m.frontend, m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanModelRows(rows)
}

type scanner func(dest ...any) error

func scanModel(scan scanner) (*modelRow, error) {
	var row modelRow
	var sock sql.NullString
	var pid sql.NullInt64
	var mem sql.NullInt64
	var maxRetries sql.NullInt64
	if err := scan(
		&row.ID, &row.Frontend, &row.Path, &row.Purpose, &row.NativeID,
		&row.Desired, &row.Observed, &row.LastError,
		&sock, &pid, &mem,
		&row.RestartPolicy, &maxRetries,
	); err != nil {
		return nil, err
	}
	if sock.Valid {
		v := sock.String
		row.SocketPath = &v
	}
	if pid.Valid {
		v := pid.Int64
		row.PID = &v
	}
	if mem.Valid {
		v := mem.Int64
		row.MemoryMB = &v
	}
	if maxRetries.Valid {
		v := int(maxRetries.Int64)
		row.RestartMaxRetries = &v
	}
	if row.RestartPolicy == "" {
		row.RestartPolicy = "unless-stopped"
	}
	return &row, nil
}

func scanModelRows(rows *sqldb.Rows) ([]modelRow, error) {
	var out []modelRow
	for rows.Next() {
		row, err := scanModel(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *row)
	}
	return out, rows.Err()
}
