package control

import (
	"context"
	"strings"

	"encore.dev/beta/errs"
)

// RouteInfo is one OpenAI-compatible route and its enablement.
type RouteInfo struct {
	Route   string `json:"route"`
	Purpose string `json:"purpose"`
	Enabled bool   `json:"enabled"`
}

// ListRoutesResponse is the public/private route listing.
type ListRoutesResponse struct {
	Routes []RouteInfo `json:"routes"`
}

// ListRoutes returns all api_routes rows (enabled flags only; no secrets).
//
//encore:api public method=GET path=/control/routes
func (s *Service) ListRoutes(ctx context.Context) (*ListRoutesResponse, error) {
	routes, err := listAPIRoutes(ctx)
	if err != nil {
		return nil, err
	}
	return &ListRoutesResponse{Routes: routes}, nil
}

// ListEnabledRoutes returns only enabled routes for the gateway.
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

// RouteEnabledParams is the query for RouteEnabled.
type RouteEnabledParams struct {
	Route   string `query:"route"`
	Purpose string `query:"purpose"`
}

// RouteEnabledResponse reports whether one (route, purpose) pair is enabled.
type RouteEnabledResponse struct {
	Route   string `json:"route"`
	Purpose string `json:"purpose"`
	Enabled bool   `json:"enabled"`
}

// RouteEnabled reports whether a single (route, purpose) row is enabled.
//
//encore:api private method=GET path=/control/routes/check
func (s *Service) RouteEnabled(ctx context.Context, p *RouteEnabledParams) (*RouteEnabledResponse, error) {
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
	return out, rows.Err()
}

func isRouteEnabled(ctx context.Context, route, purpose string) (bool, error) {
	var enabled bool
	err := db.QueryRow(ctx, `
		SELECT enabled FROM api_routes WHERE route = $1 AND purpose = $2
	`, route, purpose).Scan(&enabled)
	if err != nil {
		return false, err
	}
	return enabled, nil
}

// refreshRouteEnablement sets each api_routes.enabled from whether any
// model with that purpose is observed_state = loaded.
func refreshRouteEnablement(ctx context.Context) error {
	rows, err := db.Query(ctx, `SELECT route, purpose FROM api_routes`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type key struct{ route, purpose string }
	var keys []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.route, &k.purpose); err != nil {
			return err
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range keys {
		var n int
		err := db.QueryRow(ctx, `
			SELECT COUNT(*) FROM models
			WHERE purpose = $1 AND observed_state = 'loaded'
		`, k.purpose).Scan(&n)
		if err != nil {
			return err
		}
		enabled := n > 0
		_, err = db.Exec(ctx, `
			UPDATE api_routes
			SET enabled = $1, updated_at = NOW()
			WHERE route = $2 AND purpose = $3 AND enabled IS DISTINCT FROM $1
		`, enabled, k.route, k.purpose)
		if err != nil {
			return err
		}
	}
	return nil
}
