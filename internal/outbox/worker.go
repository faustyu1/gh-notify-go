package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrPermanent marks a failure that waiting cannot fix — the bot was kicked,
// the chat is gone, the payload is unrenderable. Wrap it to skip retries.
var ErrPermanent = errors.New("permanent delivery failure")

// maxAttempts is the number of tries before a row is abandoned. With the
// backoff schedule below this spans roughly 13 minutes.
const maxAttempts = 6

// batchSize bounds how many rows one RunOnce claims, keeping a single
// transaction short even when a backlog has built up.
const batchSize = 20

type Job struct {
	ID             int64
	IntegrationID  int64
	TelegramChatID int64
	TopicID        *int64
	Kind           string
	Payload        json.RawMessage
	Attempts       int
}

type Deliverer interface {
	Deliver(ctx context.Context, job Job) error
}

type Worker struct {
	pool      *pgxpool.Pool
	deliverer Deliverer
	now       func() time.Time
}

func NewWorker(pool *pgxpool.Pool, d Deliverer, now func() time.Time) *Worker {
	return &Worker{pool: pool, deliverer: d, now: now}
}

// Backoff maps an attempt number to the wait before the next try.
func Backoff(attempts int) time.Duration {
	schedule := []time.Duration{
		time.Second, 5 * time.Second, 30 * time.Second,
		2 * time.Minute, 10 * time.Minute, time.Hour,
	}
	if attempts < 1 {
		attempts = 1
	}
	if attempts > len(schedule) {
		return schedule[len(schedule)-1]
	}
	return schedule[attempts-1]
}

// Run drains the queue on a ticker until the context is cancelled.
func (w *Worker) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for {
				n, err := w.RunOnce(ctx)
				if err != nil {
					slog.Error("outbox run failed", "error", err)
					break
				}
				// Keep going while the queue still has work, so a backlog
				// drains without waiting a full tick per batch.
				if n < batchSize {
					break
				}
			}
		}
	}
}

// RunOnce claims a batch, delivers each job, and records the outcome. Claiming
// happens in its own short transaction; delivery happens outside it so a slow
// Telegram call never holds row locks.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	jobs, err := w.claim(ctx)
	if err != nil {
		return 0, err
	}

	for _, job := range jobs {
		deliverErr := w.deliverer.Deliver(ctx, job)
		if err := w.record(ctx, job, deliverErr); err != nil {
			return len(jobs), err
		}
	}
	return len(jobs), nil
}

// claim marks rows as in-flight. SKIP LOCKED is what lets several workers
// share one queue without coordinating: each grabs rows nobody else holds.
func (w *Worker) claim(ctx context.Context) ([]Job, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		WITH claimed AS (
			SELECT o.id
			FROM outbox o
			WHERE o.status = 'pending' AND o.scheduled_at <= $1
			ORDER BY o.scheduled_at
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE outbox o
		SET status = 'sending'
		FROM claimed, chats c
		WHERE o.id = claimed.id AND c.id = o.chat_id
		RETURNING o.id, o.integration_id, c.telegram_chat_id, c.topic_id,
		          o.event_kind, o.payload, o.attempts`,
		w.now(), batchSize)
	if err != nil {
		return nil, fmt.Errorf("claim rows: %w", err)
	}

	var jobs []Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(
			&job.ID, &job.IntegrationID, &job.TelegramChatID, &job.TopicID,
			&job.Kind, &job.Payload, &job.Attempts,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, job)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read claimed rows: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit claim: %w", err)
	}
	return jobs, nil
}

func (w *Worker) record(ctx context.Context, job Job, deliverErr error) error {
	if deliverErr == nil {
		_, err := w.pool.Exec(ctx, `
			UPDATE outbox SET status = 'sent', sent_at = $2, attempts = attempts + 1
			WHERE id = $1`, job.ID, w.now())
		if err != nil {
			return fmt.Errorf("mark sent: %w", err)
		}
		// last_event_at powers the health screen and is only meaningful for
		// messages that actually arrived.
		_, err = w.pool.Exec(ctx, `
			UPDATE integrations SET last_event_at = $2 WHERE id = $1`,
			job.IntegrationID, w.now())
		if err != nil {
			return fmt.Errorf("touch integration: %w", err)
		}
		return nil
	}

	attempts := job.Attempts + 1
	permanent := errors.Is(deliverErr, ErrPermanent) || attempts >= maxAttempts

	if permanent {
		_, err := w.pool.Exec(ctx, `
			UPDATE outbox SET status = 'failed', attempts = $2, last_error = $3
			WHERE id = $1`, job.ID, attempts, deliverErr.Error())
		if err != nil {
			return fmt.Errorf("mark failed: %w", err)
		}
		return nil
	}

	_, err := w.pool.Exec(ctx, `
		UPDATE outbox
		SET status = 'pending', attempts = $2, last_error = $3, scheduled_at = $4
		WHERE id = $1`,
		job.ID, attempts, deliverErr.Error(), w.now().Add(Backoff(attempts)))
	if err != nil {
		return fmt.Errorf("reschedule: %w", err)
	}
	return nil
}
