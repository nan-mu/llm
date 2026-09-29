package control

import "encore.dev/storage/sqldb"

var db = sqldb.NewDatabase("control", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})
