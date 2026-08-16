// Package screens holds the concrete DM screens. Each one depends on narrow
// interfaces rather than *storage.Store, so they render in tests without a
// database.
package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/domain"
	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/storage"
)

type Store interface {
	CountsForUser(ctx context.Context, userID int64) (accounts, repos, chats int, err error)
	InstallationsForUser(ctx context.Context, userID int64) ([]domain.Installation, error)
	InstallationByID(ctx context.Context, id int64) (domain.Installation, error)
	ChatsForUser(ctx context.Context, userID int64) ([]domain.ChatSummary, error)
	ChatByTelegramID(ctx context.Context, telegramChatID int64) (domain.Chat, error)
	IntegrationsInChat(ctx context.Context, chatID int64) ([]domain.Integration, error)
	EventSettings(ctx context.Context, integrationID int64) (map[string]bool, error)
	FiltersForIntegration(ctx context.Context, integrationID int64) ([]storage.Filter, error)
	StatusStats(ctx context.Context, userID int64) (sent, failed int, err error)
}

type Repos interface {
	ListRepositories(ctx context.Context, installationID int64) ([]ghapp.Repository, error)
}

type Chats interface {
	// CandidateChatsForUser, not ChatsForUser: the picker must offer chats
	// that have no integration yet, otherwise the first connect is
	// impossible.
	CandidateChatsForUser(ctx context.Context, userID int64) ([]domain.ChatSummary, error)
}
