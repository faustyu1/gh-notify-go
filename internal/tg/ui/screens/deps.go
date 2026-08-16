// Package screens holds the concrete DM screens. Each one depends on narrow
// interfaces rather than *storage.Store, so they render in tests without a
// database.
package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/domain"
	"github.com/faustyu/gh-notify-go/internal/ghapp"
)

type Store interface {
	CountsForUser(ctx context.Context, userID int64) (accounts, repos, chats int, err error)
	InstallationsForUser(ctx context.Context, userID int64) ([]domain.Installation, error)
	InstallationByID(ctx context.Context, id int64) (domain.Installation, error)
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
