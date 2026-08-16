// Package janitor bounds the tables that only ever grow. Everything it
// deletes has already served its purpose: a delivered outbox row outlives the
// rate window it is counted in, a callback key outlives the screen it belongs
// to, a dedup record outlives GitHub's retries. Without this the database
// keeps every notification ever sent, forever.
package janitor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Retention is how long each kind of spent row is kept.
type Retention struct {
	// SentOutbox covers delivered rows. The only reader is the per-chat rate
	// valve, which looks back sixty seconds, so this is margin and nothing else.
	SentOutbox time.Duration

	// FailedOutbox covers rows that ran out of attempts. The owner was already
	// told through the permanent-failure hook; the row itself is kept only long
	// enough to be looked at when someone reports a missing notification.
	FailedOutbox time.Duration

	// UIActions covers callback keys. A key older than this resolves to
	// ErrActionNotFound, which sends the user home instead of failing — so the
	// cost of expiring one is a stale button on an old screen, not a broken bot.
	UIActions time.Duration

	// AuditLog covers admin action records.
	AuditLog time.Duration

	// Deliveries covers GitHub delivery ids kept for deduplication. GitHub
	// gives up retrying long before this, so nothing older can duplicate.
	Deliveries time.Duration
}

func DefaultRetention() Retention {
	return Retention{
		SentOutbox:   time.Hour,
		FailedOutbox: 7 * 24 * time.Hour,
		UIActions:    7 * 24 * time.Hour,
		AuditLog:     90 * 24 * time.Hour,
		Deliveries:   7 * 24 * time.Hour,
	}
}

// batchSize bounds one DELETE. These tables have never been swept before, so
// the first pass may face millions of rows; chunking keeps each transaction
// short instead of locking a table for minutes.
const batchSize = 5000

type Janitor struct {
	pool      *pgxpool.Pool
	retention Retention
	now       func() time.Time
}

func New(pool *pgxpool.Pool, retention Retention, now func() time.Time) *Janitor {
	return &Janitor{pool: pool, retention: retention, now: now}
}

// Run sweeps on a ticker until the context is cancelled, starting with an
// immediate pass so a restart is a chance to catch up rather than another
// interval of growth.
func (j *Janitor) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := j.RunOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Error("janitor sweep failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce deletes everything past its retention. A failure on one table does
// not stop the others: they are independent, and a table that cannot be swept
// now is swept on the next tick.
func (j *Janitor) RunOnce(ctx context.Context) error {
	sweeps := []struct {
		name  string
		where string
		age   time.Duration
	}{
		{"outbox.sent", `status = 'sent' AND sent_at < $1`, j.retention.SentOutbox},
		{"outbox.failed", `status = 'failed' AND created_at < $1`, j.retention.FailedOutbox},
		{"ui_actions", `created_at < $1`, j.retention.UIActions},
		{"audit_log", `created_at < $1`, j.retention.AuditLog},
		{"gh_deliveries", `received_at < $1`, j.retention.Deliveries},
	}

	var firstErr error
	for _, sweep := range sweeps {
		if sweep.age <= 0 {
			continue
		}
		// "outbox.sent" and "outbox.failed" sweep one table but log apart.
		table, _, _ := strings.Cut(sweep.name, ".")
		removed, err := j.deleteOlderThan(ctx, table, sweep.where, sweep.age)
		if err != nil {
			slog.Error("janitor sweep failed", "sweep", sweep.name, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if removed > 0 {
			slog.Info("janitor swept", "sweep", sweep.name, "rows", removed)
		}
	}
	return firstErr
}

// deleteOlderThan removes matching rows in batches. ctid is the cheapest handle
// on a row that every one of these tables has in common, including the ones
// without a single-column primary key.
func (j *Janitor) deleteOlderThan(
	ctx context.Context, table, where string, age time.Duration,
) (int64, error) {
	cutoff := j.now().Add(-age)
	query := fmt.Sprintf(`
		DELETE FROM %s WHERE ctid IN (
			SELECT ctid FROM %s WHERE %s LIMIT %d
		)`, table, table, where, batchSize)

	var total int64
	for {
		tag, err := j.pool.Exec(ctx, query, cutoff)
		if err != nil {
			return total, fmt.Errorf("sweep %s: %w", table, err)
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < batchSize {
			return total, nil
		}
		// A backlog can outlast the process; stop as soon as shutdown starts
		// rather than holding it up for the rest of the sweep.
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
}
