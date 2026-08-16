package service

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/storage"
)

// InstallationInfoSource is the GitHub surface the claim path needs.
type InstallationInfoSource interface {
	InstallationInfo(ctx context.Context, installationID int64) (ghapp.Account, error)
}

// Installations ties GitHub installations to the Telegram user who owns them.
type Installations struct {
	store *storage.Store
	gh    InstallationInfoSource
}

func NewInstallations(store *storage.Store, gh InstallationInfoSource) *Installations {
	return &Installations{store: store, gh: gh}
}

// ClaimInstallation records that userID owns the installation. The user is
// identified by a single-use token the setup redirect carried, and the
// account details come from GitHub — nothing in the redirect's query string
// is trusted beyond the installation id, which is only ever used to look
// things up.
//
// An installation that already has a different owner is not transferred; see
// storage.ClaimInstallationOwner.
func (s *Installations) ClaimInstallation(
	ctx context.Context, installationID, userID int64,
) error {
	account, err := s.gh.InstallationInfo(ctx, installationID)
	if err != nil {
		return fmt.Errorf("fetch installation info: %w", err)
	}
	return s.store.ClaimInstallationOwner(ctx,
		installationID, account.Login, account.Type, userID)
}
