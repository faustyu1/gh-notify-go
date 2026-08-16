package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/outbox"
)

// recordingDeliverer captures the jobs handed to it and returns a scripted
// error, so the worker's retry decisions can be asserted directly.
type recordingDeliverer struct {
	mu   sync.Mutex
	jobs []outbox.Job
	err  error
}

func (d *recordingDeliverer) Deliver(_ context.Context, job outbox.Job) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.jobs = append(d.jobs, job)
	return d.err
}

func (d *recordingDeliverer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.jobs)
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	require.Equal(t, time.Second, outbox.Backoff(1))
	require.Equal(t, 5*time.Second, outbox.Backoff(2))
	require.Equal(t, 30*time.Second, outbox.Backoff(3))
	require.Equal(t, 2*time.Minute, outbox.Backoff(4))
	require.Equal(t, 10*time.Minute, outbox.Backoff(5))
	require.Equal(t, time.Hour, outbox.Backoff(6))
	require.Equal(t, time.Hour, outbox.Backoff(99))
}

func TestRunOnceDeliversAndMarksSent(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })
	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "push", Payload: json.RawMessage(`{"ref":"x"}`),
	})
	require.NoError(t, err)

	deliverer := &recordingDeliverer{}
	worker := outbox.NewWorker(pool, deliverer, func() time.Time { return now })

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Equal(t, 1, deliverer.count())
	require.Equal(t, int64(-100), deliverer.jobs[0].TelegramChatID)

	var status string
	var sentAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, sent_at FROM outbox WHERE id = $1`, id).Scan(&status, &sentAt))
	require.Equal(t, "sent", status)
	require.NotNil(t, sentAt)
}

func TestRunOnceIgnoresRowsScheduledInTheFuture(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })
	_, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "star", Payload: json.RawMessage(`{}`), Delay: time.Minute,
	})
	require.NoError(t, err)

	deliverer := &recordingDeliverer{}
	worker := outbox.NewWorker(pool, deliverer, func() time.Time { return now })

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.Zero(t, processed)
	require.Zero(t, deliverer.count())
}

func TestRunOnceReschedulesOnTransientFailure(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })
	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "push", Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	deliverer := &recordingDeliverer{err: errors.New("telegram timeout")}
	worker := outbox.NewWorker(pool, deliverer, func() time.Time { return now })

	_, err = worker.RunOnce(ctx)
	require.NoError(t, err)

	var (
		status      string
		attempts    int
		scheduledAt time.Time
		lastError   *string
	)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, attempts, scheduled_at, last_error FROM outbox WHERE id = $1`, id).
		Scan(&status, &attempts, &scheduledAt, &lastError))

	require.Equal(t, "pending", status)
	require.Equal(t, 1, attempts)
	require.Equal(t, now.Add(time.Second), scheduledAt.UTC())
	require.NotNil(t, lastError)
	require.Contains(t, *lastError, "telegram timeout")
}

func TestRunOnceFailsPermanentlyAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })
	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "push", Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	// Pretend five attempts already failed; this run is the sixth.
	_, err = pool.Exec(ctx, `UPDATE outbox SET attempts = 5 WHERE id = $1`, id)
	require.NoError(t, err)

	deliverer := &recordingDeliverer{err: errors.New("still failing")}
	worker := outbox.NewWorker(pool, deliverer, func() time.Time { return now })
	_, err = worker.RunOnce(ctx)
	require.NoError(t, err)

	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, id).Scan(&status))
	require.Equal(t, "failed", status)
}

func TestRunOnceDoesNotRetryPermanentErrors(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })
	id, err := queue.Enqueue(ctx, outbox.Row{
		ChatID: chatID, IntegrationID: integrationID,
		Kind: "push", Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	// Being kicked from a chat will never resolve itself by waiting.
	deliverer := &recordingDeliverer{
		err: errors.Join(outbox.ErrPermanent, errors.New("bot was kicked")),
	}
	worker := outbox.NewWorker(pool, deliverer, func() time.Time { return now })
	_, err = worker.RunOnce(ctx)
	require.NoError(t, err)

	var status string
	var attempts int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, attempts FROM outbox WHERE id = $1`, id).Scan(&status, &attempts))
	require.Equal(t, "failed", status)
	require.Equal(t, 1, attempts)
}

func TestConcurrentWorkersDoNotDeliverTheSameRowTwice(t *testing.T) {
	ctx := context.Background()
	pool, chatID, integrationID := fixture(t)

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	queue := outbox.NewQueue(pool, func() time.Time { return now })
	for range 20 {
		_, err := queue.Enqueue(ctx, outbox.Row{
			ChatID: chatID, IntegrationID: integrationID,
			Kind: "push", Payload: json.RawMessage(`{}`),
		})
		require.NoError(t, err)
	}

	deliverer := &recordingDeliverer{}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker := outbox.NewWorker(pool, deliverer, func() time.Time { return now })
			for {
				n, err := worker.RunOnce(ctx)
				if err != nil || n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()

	require.Equal(t, 20, deliverer.count(), "each row must be delivered exactly once")
}
