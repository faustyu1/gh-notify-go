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

// ClaimInstallation records that userID (users.id, carried through the
// install URL as `state`) owns the installation. The account details come
// from GitHub, not from the unauthenticated redirect.
func (s *Installations) ClaimInstallation(
	ctx context.Context, installationID, userID int64,
) error {
	account, err := s.gh.InstallationInfo(ctx, installationID)
	if err != nil {
		return fmt.Errorf("fetch installation info: %w", err)
	}
	if _, err := s.store.UpsertInstallation(ctx,
		installationID, account.Login, account.Type, userID); err != nil {
		return fmt.Errorf("claim installation: %w", err)
	}
	return nil
}
