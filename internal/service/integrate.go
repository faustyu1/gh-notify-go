package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/faustyu/gh-notify-go/internal/storage"
)

var (
	ErrNotAdmin         = errors.New("user is not an administrator of this chat")
	ErrAlreadyConnected = errors.New("repository is already connected to this chat")
)

// AdminChecker answers whether a user may change a chat's integrations. It is
// an interface so the check can be faked in tests and cached in production.
type AdminChecker interface {
	IsAdmin(ctx context.Context, telegramChatID, telegramUserID int64) (bool, error)
}

type ConnectRequest struct {
	UserID         int64
	TelegramUserID int64
	InstallationID int64
	ChatID         int64
	TelegramChatID int64
	RepoGitHubID   int64
	RepoFullName   string
}

type Integrator struct {
	store *storage.Store
	admin AdminChecker
}

func NewIntegrator(store *storage.Store, admin AdminChecker) *Integrator {
	return &Integrator{store: store, admin: admin}
}

// Connect wires a repository to a chat. The admin check happens here, at the
// moment of the action — checking only when the menu was drawn would let a
// demoted user act on a stale screen.
func (i *Integrator) Connect(ctx context.Context, req ConnectRequest) error {
	ok, err := i.admin.IsAdmin(ctx, req.TelegramChatID, req.TelegramUserID)
	if err != nil {
		return fmt.Errorf("check admin rights: %w", err)
	}
	if !ok {
		return ErrNotAdmin
	}

	_, err = i.store.CreateIntegration(ctx,
		req.ChatID, req.InstallationID, req.RepoGitHubID, req.RepoFullName, req.UserID)
	if err != nil {
		var pgErr *pgconn.PgError
		// 23505 is unique_violation: the (chat_id, repo_github_id) pair.
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrAlreadyConnected
		}
		return err
	}

	meta, _ := json.Marshal(map[string]any{
		"repo":         req.RepoFullName,
		"installation": req.InstallationID,
	})
	if _, err := i.store.Pool().Exec(ctx, `
		INSERT INTO audit_log (actor_user_id, chat_id, action, meta)
		VALUES ($1, $2, 'integration.create', $3)`,
		req.UserID, req.ChatID, meta); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}
