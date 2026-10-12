package market

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB is the subset of *pgxpool.Pool and pgx.Tx the market service queries
// use (same seam shape as the wallet's).
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Pool is DB plus transaction opening (*pgxpool.Pool satisfies it).
type Pool interface {
	DB
	Begin(ctx context.Context) (pgx.Tx, error)
}
