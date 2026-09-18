package control

import (
	"context"
	"strings"

	"encore.app/internal/modelstate"
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
	Route string `query:"route"`
}

// RouteEnabledResponse reports whether one route is enabled.
type RouteEnabledResponse struct {
	Route   string `json:"route"`
	Enabled bool   `json:"enabled"`
}

// RouteEnabled reports whether a single OpenAI route is enabled.
//
//encore:api private method=GET path=/control/routes/check
func (s *Service) RouteEnabled(ctx context.Context, p *RouteEnabledParams) (*RouteEnabledResponse, error) {
	route := strings.TrimSpace(p.Route)
	if route == "" {
		return nil, &errs.Error{Code: errs.InvalidArgument, Message: "route required"}
	}
	enabled, err := isRouteEnabled(ctx, route)
	if err != nil {
		return nil, err
	}
	return &RouteEnabledResponse{Route: route, Enabled: enabled}, nil
}

func listAPIRoutes(ctx context.Context) ([]RouteInfo, error) {
	rows, err := db.Query(ctx, `
		SELECT route, purpose, enabled
		FROM api_routes
		ORDER BY route
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

func isRouteEnabled(ctx context.Context, route string) (bool, error) {
	var enabled bool
	err := db.QueryRow(ctx, `
		SELECT enabled FROM api_routes WHERE route = $1
	`, route).Scan(&enabled)
	if err != nil {
		return false, err
	}
	return enabled, nil
}

// refreshRouteEnablement sets each api_routes.enabled from whether any
// model with that purpose is observed_state = loaded.
func refreshRouteEnablement(ctx context.Context) error {
	purposes := []modelstate.Purpose{modelstate.PurposeTranslation, modelstate.PurposeASR}
	for _, purpose := range purposes {
		var n int
		err := db.QueryRow(ctx, `
			SELECT COUNT(*) FROM models
			WHERE purpose = $1 AND observed_state = 'loaded'
		`, string(purpose)).Scan(&n)
		if err != nil {
			return err
		}
		enabled := n > 0
		_, err = db.Exec(ctx, `
			UPDATE api_routes
			SET enabled = $1, updated_at = NOW()
			WHERE purpose = $2 AND enabled IS DISTINCT FROM $1
		`, enabled, string(purpose))
		if err != nil {
			return err
		}
	}
	return nil
}
