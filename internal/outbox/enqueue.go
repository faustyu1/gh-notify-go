// Package outbox is the durable delivery queue. The webhook handler writes to
// it and answers GitHub; workers drain it and talk to Telegram. Nothing else
// bridges those two sides.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Row struct {
	ChatID        int64
	IntegrationID int64
	Kind          string
	Payload       json.RawMessage

	// GroupKey ties rows that may cancel or merge with each other, such as
	// every pending star from one actor on one integration. Empty means the
	// row stands alone.
	GroupKey string

	// Delay holds the row back, which is what makes the star debounce
	// possible: the row exists but is not yet eligible for delivery.
	Delay time.Duration
}

type Queue struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewQueue(pool *pgxpool.Pool, now func() time.Time) *Queue {
	return &Queue{pool: pool, now: now}
}

// MarkDelivered records a GitHub delivery id and reports whether it is new.
// GitHub retries deliveries, so this is what keeps one push from producing
// two messages.
func (q *Queue) MarkDelivered(ctx context.Context, deliveryID string) (bool, error) {
	tag, err := q.pool.Exec(ctx, `
		INSERT INTO gh_deliveries (delivery_id) VALUES ($1)
		ON CONFLICT (delivery_id) DO NOTHING`, deliveryID)
	if err != nil {
		return false, fmt.Errorf("record delivery: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (q *Queue) Enqueue(ctx context.Context, row Row) (int64, error) {
	var groupKey *string
	if row.GroupKey != "" {
		groupKey = &row.GroupKey
	}

	var id int64
	err := q.pool.QueryRow(ctx, `
		INSERT INTO outbox
			(chat_id, integration_id, event_kind, payload, group_key, scheduled_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		row.ChatID, row.IntegrationID, row.Kind, []byte(row.Payload),
		groupKey, q.now().Add(row.Delay),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("enqueue: %w", err)
	}
	return id, nil
}

// PruneDeliveries drops dedup records past their usefulness. GitHub gives up
// retrying long before this window, so anything older cannot cause a
// duplicate.
func (q *Queue) PruneDeliveries(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := q.pool.Exec(ctx,
		`DELETE FROM gh_deliveries WHERE received_at < $1`, q.now().Add(-olderThan))
	if err != nil {
		return 0, fmt.Errorf("prune deliveries: %w", err)
	}
	return tag.RowsAffected(), nil
}
