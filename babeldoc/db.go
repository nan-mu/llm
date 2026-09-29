package babeldoc

import "encore.dev/storage/sqldb"

// MaxUploadBytes is the source PDF cap for one task.
const MaxUploadBytes int64 = 100 << 20

var db = sqldb.NewDatabase("babeldoc", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})
