// Package migrations embeds the goose schema migrations and applies them.
package migrations

import (
	"context"
	"embed"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var FS embed.FS

// lockID identifies this application's migration lock. Any constant works as
// long as it is ours alone; advisory locks share one namespace per database.
const lockID = 8_476_213_004_915_772

// Up applies every pending migration. It opens its own database/sql handle
// because goose needs one; the application itself uses pgxpool.
//
// A session advisory lock serialises the whole thing: two processes starting
// at once — a rolling deploy, a restart racing a new container — would
// otherwise both try to apply the same migration.
func Up(ctx context.Context, dbURL string) error {
	cfg, err := pgx.ParseConfig(dbURL)
	if err != nil {
		return fmt.Errorf("invalid database url: %w", err)
	}
	db := stdlib.OpenDB(*cfg)
	defer func() { _ = db.Close() }()

	// The lock lives on one connection, so it has to be held on one
	// connection: a pooled handle could unlock from a different session.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open migration connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return fmt.Errorf("take migration lock: %w", err)
	}
	defer func() {
		if _, err := conn.ExecContext(
			context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, lockID); err != nil {
			slog.Warn("release migration lock", "error", err)
		}
	}()

	goose.SetBaseFS(FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
