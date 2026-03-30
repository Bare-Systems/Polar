package storage

import (
	"database/sql"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"polar/internal/config"
)

func Open(cfg config.StorageConfig) (*sql.DB, string, error) {
	driver := strings.TrimSpace(strings.ToLower(cfg.Driver))
	if driver == "" {
		driver = DialectSQLite
	}

	switch driver {
	case DialectPostgres, "pgx":
		db, err := sql.Open("pgx", cfg.DatabaseURL)
		return db, DialectPostgres, err
	default:
		path := cfg.SQLitePath
		if path == "" {
			path = "./polar.db"
		}
		db, err := sql.Open("sqlite", path)
		return db, DialectSQLite, err
	}
}
