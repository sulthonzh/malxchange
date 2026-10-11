// Package pgmig runs embedded SQL migrations at service boot via
// golang-migrate (pgx5 driver). Migrations land inside the service's own
// schema because the connection's search_path is pinned there.
package pgmig

import (
	"fmt"
	"io/fs"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// Run applies all up-migrations from sub (a directory inside migrationsFS,
// e.g. "wallet") to the database at databaseURL. It is safe to call at
// every boot: applied versions are skipped.
func Run(databaseURL string, migrationsFS fs.FS, sub string) error {
	src, err := iofs.New(migrationsFS, sub)
	if err != nil {
		return fmt.Errorf("pgmig: open %s: %w", sub, err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, withScheme(databaseURL, "pgx5"))
	if err != nil {
		return fmt.Errorf("pgmig: init: %w", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("pgmig: up: %w", err)
	}
	return nil
}

// withScheme rewrites postgres://… into pgx5://…, preserving the query
// string (search_path) that pins the target schema.
func withScheme(databaseURL, scheme string) string {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return databaseURL // let the driver report the parse error
	}
	u.Scheme = scheme
	return u.String()
}
