package gateway

import (
	"context"

	"encore.dev/storage/sqldb"
)

//encore:service
type Service struct{}

var db = sqldb.NewDatabase("gateway", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

func initService() (*Service, error) {
	return &Service{}, nil
}

// Health reports that the gateway service is up.
//
//encore:api public method=GET path=/health
func (s *Service) Health(ctx context.Context) error {
	var n int
	return db.QueryRow(ctx, "SELECT 1").Scan(&n)
}
