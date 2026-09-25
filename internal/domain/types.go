// Package domain holds the types shared across services. It imports no
// infrastructure, so services built on it are testable without a database.
package domain

import "time"

type Chat struct {
	ID             int64
	TelegramChatID int64
	Title          string
	Kind           string
	IsForum        bool
	TopicID        *int64
	MutedUntil     *time.Time
}

// Integration is the denormalized view the delivery path needs: enough to
// address a Telegram message and to notify the owner when delivery fails,
// without a second query per event.
type Integration struct {
	ID                   int64
	ChatID               int64
	TelegramChatID       int64
	TopicID              *int64
	InstallationID       int64
	GitHubInstallationID int64
	Provider             string
	RepoGitHubID         int64
	RepoFullName         string
	CreatedByUserID      int64
	OwnerTelegramID      int64
	BrokenReason         *string
}

// Provider names where an installation's repositories live.
const (
	ProviderGitHub = "github"
	ProviderGitLab = "gitlab"
)

// Installation is one source of repositories: a GitHub App installation, or
// a GitLab webhook connection. GitHubInstallationID is zero for GitLab.
type Installation struct {
	ID                   int64
	GitHubInstallationID int64
	Provider             string
	AccountLogin         string
	AccountType          string
	Suspended            bool
}

// ChatSummary is what the chats screen lists: one row per chat with enough
// context to pick the right one.
type ChatSummary struct {
	ChatID           int64
	TelegramChatID   int64
	Title            string
	IntegrationCount int
}
