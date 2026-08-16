package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
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
	ChatID         int64
	IntegrationID  int64
	TelegramChatID int64
	TopicID        *int64
	Kind           string
	Payload        json.RawMessage
	Attempts       int

	// Language is the integration owner's locale: notifications in a chat
	// speak the language of whoever set up that delivery, resolved fresh on
	// every claim so a settings change applies to future messages.
	Language string
}

type Deliverer interface {
	Deliver(ctx context.Context, job Job) error
}

// PermanentHook is called once for a row that will never be retried — the
// integration owner must hear about it.
type PermanentHook func(ctx context.Context, job Job, err error)

type Worker struct {
	pool          *pgxpool.Pool
	deliverer     Deliverer
	now           func() time.Time
	chatPerMinute int
	onPermanent   PermanentHook
}

func NewWorker(pool *pgxpool.Pool, d Deliverer, now func() time.Time) *Worker {
	return &Worker{pool: pool, deliverer: d, now: now}
}

// WithChatPerMinute arms the per-chat rate valve: beyond this many delivered
// messages per minute, further rows collapse into one digest per chat.
func (w *Worker) WithChatPerMinute(limit int) *Worker {
	w.chatPerMinute = limit
	return w
}

// WithOnPermanent registers the terminal-failure hook.
func (w *Worker) WithOnPermanent(hook PermanentHook) *Worker {
	w.onPermanent = hook
	return w
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
		if w.chatPerMinute > 0 && job.Kind != "digest" {
			over, err := w.chatOverBudget(ctx, job.ChatID)
			if err != nil {
				return len(jobs), err
			}
			if over {
				if err := w.digest(ctx, job); err != nil {
					return len(jobs), err
				}
				continue
			}
		}

		deliverErr := w.deliverer.Deliver(ctx, job)
		if err := w.record(ctx, job, deliverErr); err != nil {
			return len(jobs), err
		}
	}
	return len(jobs), nil
}

// chatOverBudget counts messages this chat received in the last minute. The
// check races with concurrent workers by a row or two, which the valve is
// coarse enough to absorb.
func (w *Worker) chatOverBudget(ctx context.Context, chatID int64) (bool, error) {
	var sent int
	err := w.pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox
		WHERE chat_id = $1 AND status = 'sent'
		  AND sent_at > $2::timestamptz - interval '60 seconds'`,
		chatID, w.now()).Scan(&sent)
	if err != nil {
		return false, fmt.Errorf("count sent: %w", err)
	}
	return sent >= w.chatPerMinute, nil
}

// digest merges an over-budget row into the chat's single pending digest row:
// one summary message instead of N more. Sustained flooding keeps merging,
// but the digest goes out once the minute calms down.
func (w *Worker) digest(ctx context.Context, job Job) error {
	groupKey := fmt.Sprintf("digest:%d", job.ChatID)

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin digest: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		rowID   int64
		rawJSON []byte
		payload struct {
			Items []string `json:"items"`
		}
	)
	err = tx.QueryRow(ctx, `
		SELECT id, payload FROM outbox
		WHERE group_key = $1 AND status = 'pending'
		FOR UPDATE`, groupKey).Scan(&rowID, &rawJSON)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		payload.Items = []string{job.Kind}
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE outbox
			SET event_kind = 'digest', payload = $2, group_key = $3,
			    status = 'pending', scheduled_at = $4
			WHERE id = $1`,
			job.ID, raw, groupKey, w.now().Add(time.Minute)); err != nil {
			return fmt.Errorf("create digest: %w", err)
		}
	case err != nil:
		return fmt.Errorf("load digest: %w", err)
	default:
		if err := json.Unmarshal(rawJSON, &payload); err != nil {
			return fmt.Errorf("decode digest: %w", err)
		}
		// Cap growth: a digest bigger than this is absurd, and keeping the
		// original schedule stops sustained floods from postponing it forever.
		if len(payload.Items) < 50 {
			payload.Items = append(payload.Items, job.Kind)
			if err := tx.QueryRow(ctx, `
				UPDATE outbox SET payload = $2
				WHERE id = $1 RETURNING id`, rowID, mustJSON(payload)).Scan(&rowID); err != nil {
				return fmt.Errorf("merge digest: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE outbox SET scheduled_at = $2 WHERE id = $1`,
				rowID, w.now().Add(time.Minute)); err != nil {
				return fmt.Errorf("postpone digest: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE id = $1`, job.ID); err != nil {
			return fmt.Errorf("digest source row: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
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
		FROM claimed, chats c, integrations i, users u
		WHERE o.id = claimed.id
		  AND c.id = o.chat_id
		  AND i.id = o.integration_id
		  AND u.id = i.created_by_user_id
		RETURNING o.id, o.chat_id, o.integration_id,
		          c.telegram_chat_id, c.topic_id, u.language,
		          o.event_kind, o.payload, o.attempts`,
		w.now(), batchSize)
	if err != nil {
		return nil, fmt.Errorf("claim rows: %w", err)
	}

	var jobs []Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(
			&job.ID, &job.ChatID, &job.IntegrationID,
			&job.TelegramChatID, &job.TopicID, &job.Language,
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
		if w.onPermanent != nil {
			w.onPermanent(ctx, job, deliverErr)
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
