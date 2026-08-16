package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// StarActorNotified reports whether this actor already produced a star
// notification for this integration. The rule is one per actor forever:
// toggling a star must not turn into a notification stream.
func (s *Store) StarActorNotified(ctx context.Context, integrationID int64, actor string) (bool, error) {
	var marker string
	err := s.pool.QueryRow(ctx, `
		SELECT 'x' FROM star_actors
		WHERE integration_id = $1 AND actor_login = $2`, integrationID, actor).Scan(&marker)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read star actor: %w", err)
	}
	return true, nil
}

// MarkStarNotified records the actor so every later star from them is silent.
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
