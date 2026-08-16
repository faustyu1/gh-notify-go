package ui

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrActionNotFound = errors.New("ui action not found")

// frame is one entry on the navigation stack.
type frame struct {
	Screen string `json:"s"`
	Params Params `json:"p,omitempty"`
}

// NavStore persists per-user navigation state. It lives in the database so
// ◁ Назад keeps working across a bot restart.
type NavStore interface {
	Push(ctx context.Context, userID int64, screen string, params Params) error
	Pop(ctx context.Context, userID int64) (screen string, params Params, err error)
	Depth(ctx context.Context, userID int64) (int, error)
	PutAction(ctx context.Context, userID int64, key, screen string, params Params) error
	GetAction(ctx context.Context, userID int64, key string) (screen string, params Params, err error)
	AnchorMessageID(ctx context.Context, userID int64) (int, error)
	SetAnchorMessageID(ctx context.Context, userID int64, messageID int) error
}

type postgresNav struct{ pool *pgxpool.Pool }

func NewPostgresNav(pool *pgxpool.Pool) NavStore { return postgresNav{pool: pool} }

func (n postgresNav) load(ctx context.Context, userID int64) ([]frame, error) {
	var raw []byte
	err := n.pool.QueryRow(ctx,
		`SELECT stack FROM ui_nav WHERE user_id = $1`, userID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load nav stack: %w", err)
	}

	var stack []frame
	if err := json.Unmarshal(raw, &stack); err != nil {
		return nil, fmt.Errorf("decode nav stack: %w", err)
	}
	return stack, nil
}

func (n postgresNav) save(ctx context.Context, userID int64, stack []frame) error {
	raw, err := json.Marshal(stack)
	if err != nil {
		return fmt.Errorf("encode nav stack: %w", err)
	}
	_, err = n.pool.Exec(ctx, `
		INSERT INTO ui_nav (user_id, stack, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE SET stack = EXCLUDED.stack, updated_at = now()`,
		userID, raw)
	if err != nil {
		return fmt.Errorf("save nav stack: %w", err)
	}
	return nil
}

// Push replaces the whole stack when the target is "home", so returning to
// the root cannot leave an ever-growing history behind.
func (n postgresNav) Push(ctx context.Context, userID int64, screen string, params Params) error {
	if screen == "home" {
		return n.save(ctx, userID, []frame{{Screen: screen, Params: params}})
	}

	stack, err := n.load(ctx, userID)
	if err != nil {
		return err
	}
	return n.save(ctx, userID, append(stack, frame{Screen: screen, Params: params}))
}

// Pop removes the current frame and returns the one beneath it. At the root
// it returns the root again, so ◁ Назад is never a dead end.
func (n postgresNav) Pop(ctx context.Context, userID int64) (string, Params, error) {
	stack, err := n.load(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	if len(stack) == 0 {
		return "home", nil, nil
	}
	if len(stack) == 1 {
		return stack[0].Screen, stack[0].Params, nil
	}

	stack = stack[:len(stack)-1]
	if err := n.save(ctx, userID, stack); err != nil {
		return "", nil, err
	}
	top := stack[len(stack)-1]
	return top.Screen, top.Params, nil
}

func (n postgresNav) Depth(ctx context.Context, userID int64) (int, error) {
	stack, err := n.load(ctx, userID)
	if err != nil {
		return 0, err
	}
	return len(stack), nil
}

func (n postgresNav) PutAction(
	ctx context.Context, userID int64, key, screen string, params Params,
) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode action params: %w", err)
	}
	_, err = n.pool.Exec(ctx, `
		INSERT INTO ui_actions (key, user_id, screen, params) VALUES ($1, $2, $3, $4)`,
		key, userID, screen, raw)
	if err != nil {
		return fmt.Errorf("store action: %w", err)
	}
	return nil
}

// GetAction scopes the lookup to the user, so a callback key leaked from one
// chat cannot drive another user's session.
func (n postgresNav) GetAction(
	ctx context.Context, userID int64, key string,
) (string, Params, error) {
	var (
		screen string
		raw    []byte
	)
	err := n.pool.QueryRow(ctx, `
		SELECT screen, params FROM ui_actions WHERE key = $1 AND user_id = $2`,
		key, userID).Scan(&screen, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrActionNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("load action: %w", err)
	}

	var params Params
	if err := json.Unmarshal(raw, &params); err != nil {
		return "", nil, fmt.Errorf("decode action params: %w", err)
	}
	return screen, params, nil
}

func (n postgresNav) AnchorMessageID(ctx context.Context, userID int64) (int, error) {
	var messageID *int64
	err := n.pool.QueryRow(ctx,
		`SELECT message_id FROM ui_nav WHERE user_id = $1`, userID).Scan(&messageID)
	if errors.Is(err, pgx.ErrNoRows) || messageID == nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load anchor message: %w", err)
	}
	return int(*messageID), nil
}

func (n postgresNav) SetAnchorMessageID(ctx context.Context, userID int64, messageID int) error {
	_, err := n.pool.Exec(ctx, `
		INSERT INTO ui_nav (user_id, message_id) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET message_id = EXCLUDED.message_id`,
		userID, messageID)
	if err != nil {
		return fmt.Errorf("save anchor message: %w", err)
	}
	return nil
}

// newActionKey produces a short opaque token. 12 random bytes base64url is 16
// characters, comfortably inside Telegram's 64-byte callback_data limit no
// matter how large the params are.
func newActionKey() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate action key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
