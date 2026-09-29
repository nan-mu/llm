package control

import (
	"context"
	"errors"
	"strings"

	"encore.dev/beta/errs"
	"encore.dev/storage/sqldb"
)

// RouteInfo is one OpenAI-compatible route and its enablement.
type RouteInfo struct {
	Route   string `json:"route"`
	Purpose string `json:"purpose"`
	Enabled bool   `json:"enabled"`
}

// ListRoutesResponse is the route listing. It has no secrets or socket paths.
type ListRoutesResponse struct {
	Routes []RouteInfo `json:"routes"`
}

// ListRoutes returns every api_routes row.
//
//encore:api public method=GET path=/control/routes
func (s *Service) ListRoutes(ctx context.Context) (*ListRoutesResponse, error) {
	routes, err := listAPIRoutes(ctx)
	if err != nil {
		return nil, err
	}
	return &ListRoutesResponse{Routes: routes}, nil
}

// ListEnabledRoutes returns only enabled routes.
//
//encore:api private method=GET path=/control/routes/enabled
func (s *Service) ListEnabledRoutes(ctx context.Context) (*ListRoutesResponse, error) {
	all, err := listAPIRoutes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RouteInfo, 0, len(all))
	for _, r := range all {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return &ListRoutesResponse{Routes: out}, nil
}

// RouteEnabledParams selects one (route, purpose) pair.
type RouteEnabledParams struct {
	Route   string `query:"route"`
	Purpose string `query:"purpose"`
}

// RouteEnabledResponse reports whether that pair is enabled.
type RouteEnabledResponse struct {
	Route   string `json:"route"`
	Purpose string `json:"purpose"`
	Enabled bool   `json:"enabled"`
}

// RouteEnabled reports whether a single (route, purpose) row is enabled.
// Gateway must use this (or ListEnabledRoutes) instead of scanning models.
//
//encore:api private method=GET path=/control/routes/check
func (s *Service) RouteEnabled(ctx context.Context, p *RouteEnabledParams) (*RouteEnabledResponse, error) {
	if p == nil {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "route required"}
	}
	route := strings.TrimSpace(p.Route)
	purpose := strings.TrimSpace(p.Purpose)
	if route == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "route required"}
	}
	if purpose == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "purpose required"}
	}
	enabled, err := isRouteEnabled(ctx, route, purpose)
	if err != nil {
		return nil, err
	}
	return &RouteEnabledResponse{Route: route, Purpose: purpose, Enabled: enabled}, nil
}

func listAPIRoutes(ctx context.Context) ([]RouteInfo, error) {
	rows, err := db.Query(ctx, `
		SELECT route, purpose, enabled
		FROM api_routes
		ORDER BY route, purpose
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RouteInfo
	for rows.Next() {
		var r RouteInfo
		if err := rows.Scan(&r.Route, &r.Purpose, &r.Enabled); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if out == nil {
		out = []RouteInfo{}
	}
	return out, rows.Err()
}

func isRouteEnabled(ctx context.Context, route, purpose string) (bool, error) {
	var enabled bool
	err := db.QueryRow(ctx, `
		SELECT enabled FROM api_routes WHERE route = $1 AND purpose = $2
	`, route, purpose).Scan(&enabled)
	if errors.Is(err, sqldb.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled, nil
}

// refreshRouteEnablement sets enabled from whether any model with that purpose is loaded.
func refreshRouteEnablement(ctx context.Context) error {
	_, err := db.Exec(ctx, `
		UPDATE api_routes ar
		SET enabled = EXISTS (
			SELECT 1 FROM models m
			WHERE m.purpose = ar.purpose AND m.observed_state = 'loaded'
		),
		updated_at = NOW()
	`)
	return err
}
