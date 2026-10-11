// Package pg connects a service to Postgres under its own role and schema
// (ADR-0001: one database, one schema + one role per service; no
// cross-service JOINs are possible because grants never cross schemas).
package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/config"
)

// Connect opens a pool pinned to the service schema and ensures the schema
// exists (the role holds CREATE on the database, granted by the Postgres
// init script; schema creation is idempotent so it doubles for tests).
func Connect(ctx context.Context, cfg config.PG) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, cfg.URL())
	if err != nil {
		return nil, fmt.Errorf("pg: parse config: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg: ping: %w", err)
	}
	// cfg.Schema was validated as a plain identifier by config.Load.
	if _, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", cfg.Schema)); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg: ensure schema %s: %w", cfg.Schema, err)
	}
	return pool, nil
}
