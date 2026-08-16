package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// StarPendingAddActor adds an actor to the one debounced star row per
// integration, creating it if absent. The row's scheduled_at is pushed out to
// now+debounce on every star, which is the net-effect window: as long as stars
// keep arriving, nothing is sent.
func (s *Store) StarPendingAddActor(
	ctx context.Context, chatID, integrationID int64,
	actor string, repoFullName string, totalStars int, debounce time.Duration,
) error {
	groupKey := fmt.Sprintf("star:%d", integrationID)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin star pending: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		rowID   int64
		rawJS   []byte
		payload starPayload
	)
	err = tx.QueryRow(ctx, `
		SELECT id, payload FROM outbox
		WHERE group_key = $1 AND status = 'pending'
		FOR UPDATE`, groupKey).Scan(&rowID, &rawJS)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		payload = starPayload{
			RepoFullName: repoFullName,
			Actors:       []string{actor},
			TotalStars:   totalStars,
		}
	case err != nil:
		return fmt.Errorf("load star pending: %w", err)
	default:
		if err := json.Unmarshal(rawJS, &payload); err != nil {
			return fmt.Errorf("decode star pending: %w", err)
		}
		payload.Actors = appendUnique(payload.Actors, actor)
		payload.TotalStars = totalStars
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode star pending: %w", err)
	}

	if rowID == 0 {
		_, err = tx.Exec(ctx, `
			INSERT INTO outbox
				(chat_id, integration_id, event_kind, payload, group_key, scheduled_at)
			VALUES ($1, $2, 'star', $3, $4, $5)`,
			chatID, integrationID, raw, groupKey, s.now().Add(debounce))
	} else {
		_, err = tx.Exec(ctx, `
			UPDATE outbox SET payload = $2, scheduled_at = $3 WHERE id = $1`,
			rowID, raw, s.now().Add(debounce))
	}
	if err != nil {
		return fmt.Errorf("write star pending: %w", err)
	}
	return tx.Commit(ctx)
}

// StarPendingRemoveActor takes an actor back out of the pending row — the
// unstar arrived inside the debounce window, so the star nets out to nothing.
// An empty row is deleted outright.
func (s *Store) StarPendingRemoveActor(ctx context.Context, integrationID int64, actor string) error {
	groupKey := fmt.Sprintf("star:%d", integrationID)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin star cancel: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		rowID   int64
		rawJS   []byte
		payload starPayload
	)
	err = tx.QueryRow(ctx, `
		SELECT id, payload FROM outbox
		WHERE group_key = $1 AND status = 'pending'
		FOR UPDATE`, groupKey).Scan(&rowID, &rawJS)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // nothing pending: the star already went out or never existed
	}
	if err != nil {
		return fmt.Errorf("load star cancel: %w", err)
	}
	if err := json.Unmarshal(rawJS, &payload); err != nil {
		return fmt.Errorf("decode star cancel: %w", err)
	}

	payload.Actors = removeValue(payload.Actors, actor)
	if len(payload.Actors) == 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE id = $1`, rowID); err != nil {
			return fmt.Errorf("delete star cancel: %w", err)
		}
		return tx.Commit(ctx)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode star cancel: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE outbox SET payload = $2 WHERE id = $1`, rowID, raw); err != nil {
		return fmt.Errorf("update star cancel: %w", err)
	}
	return tx.Commit(ctx)
}

type starPayload struct {
	RepoFullName string   `json:"repo_full_name"`
	Actors       []string `json:"actors"`
	TotalStars   int      `json:"total_stars"`
}

// StarCooldownActive reports whether this actor already produced a star
// notification for this integration inside the cooldown window.
func (s *Store) StarCooldownActive(
	ctx context.Context, integrationID int64, actor string, cooldown time.Duration,
) (bool, error) {
	var last time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT last_notified_at FROM star_actors
		WHERE integration_id = $1 AND actor_login = $2`, integrationID, actor).Scan(&last)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read star cooldown: %w", err)
	}
	return s.now().Sub(last) < cooldown, nil
}

// MarkStarNotified stamps the cooldown clock for an actor.
func (s *Store) MarkStarNotified(ctx context.Context, integrationID int64, actor string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO star_actors (integration_id, actor_login, last_notified_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (integration_id, actor_login) DO UPDATE
		SET last_notified_at = EXCLUDED.last_notified_at`,
		integrationID, actor, s.now())
	if err != nil {
		return fmt.Errorf("mark star notified: %w", err)
	}
	return nil
}

// ClearStarNotified rewinds the clock when a star is cancelled before
// delivery: the actor netted out and must not be silenced for it.
func (s *Store) ClearStarNotified(ctx context.Context, integrationID int64, actor string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE star_actors SET last_notified_at = 'epoch'
		WHERE integration_id = $1 AND actor_login = $2`, integrationID, actor)
	if err != nil {
		return fmt.Errorf("clear star notified: %w", err)
	}
	return nil
}

// SetPendingInput remembers which ForceReply action the user's next message
// feeds (set topic, add filter) and with what context.
func (s *Store) SetPendingInput(
	ctx context.Context, userID int64, action string, params ui.Params,
) error {
	raw, err := json.Marshal(map[string]any{"action": action, "params": params})
	if err != nil {
		return fmt.Errorf("encode pending input: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO ui_nav (user_id, pending) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET pending = EXCLUDED.pending`,
		userID, raw)
	if err != nil {
		return fmt.Errorf("save pending input: %w", err)
	}
	return nil
}

// TakePendingInput returns and clears the user's pending input, if any.
func (s *Store) TakePendingInput(ctx context.Context, userID int64) (string, ui.Params, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `
		UPDATE ui_nav SET pending = NULL
		WHERE user_id = $1 AND pending IS NOT NULL
		RETURNING pending`, userID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("take pending input: %w", err)
	}

	var pending struct {
		Action string    `json:"action"`
		Params ui.Params `json:"params"`
	}
	if err := json.Unmarshal(raw, &pending); err != nil {
		return "", nil, fmt.Errorf("decode pending input: %w", err)
	}
	return pending.Action, pending.Params, nil
}

func appendUnique(values []string, value string) []string {
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}

func removeValue(values []string, value string) []string {
	out := values[:0]
	for _, v := range values {
		if v != value {
			out = append(out, v)
		}
	}
	return out
}
