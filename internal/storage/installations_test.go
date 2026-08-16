package storage_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/storage"
)

func TestRegisterInstallationCreatesOwnerlessRow(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	require.NoError(t, store.RegisterInstallation(ctx, 154234486, "faustyu1", "User"))

	var owner *int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT user_id FROM installations WHERE github_installation_id = $1`,
		154234486).Scan(&owner))
	require.Nil(t, owner)

	// An unclaimed installation belongs to nobody and must not leak into
	// anyone's accounts screen.
	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	found, err := store.InstallationsForUser(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestRegisterInstallationKeepsExistingOwner(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	mustInstallation(t, store, 154234486, "faustyu1", "User", userID)

	// A later webhook (e.g. new_permissions_accepted) must not steal ownership.
	require.NoError(t, store.RegisterInstallation(ctx, 154234486, "faustyu1", "User"))

	found, err := store.InstallationsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "faustyu1", found[0].AccountLogin)
}

func TestSetInstallationSuspendedToggles(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	mustInstallation(t, store, 7, "acme", "Organization", userID)

	require.NoError(t, store.SetInstallationSuspended(ctx, 7, true))
	found, _ := store.InstallationsForUser(ctx, userID)
	require.True(t, found[0].Suspended)

	require.NoError(t, store.SetInstallationSuspended(ctx, 7, false))
	found, _ = store.InstallationsForUser(ctx, userID)
	require.False(t, found[0].Suspended)
}

func TestDeleteInstallationCascadesToIntegrations(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	installID := mustInstallation(t, store, 7, "acme", "Organization", userID)
	_, _ = store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)

	require.NoError(t, store.DeleteInstallation(ctx, 7))

	var count int
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM integrations`).Scan(&count))
	require.Zero(t, count, "uninstalling the App must remove its integrations")
}

func TestClaimInstallationOwnerTakesAnOwnerlessInstallation(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	require.NoError(t, store.RegisterInstallation(ctx, 7, "acme", "Organization"))

	require.NoError(t, store.ClaimInstallationOwner(ctx, 7, "acme", "Organization", userID))

	found, err := store.InstallationsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, found, 1)
}

// Installation ids are neither secret nor unguessable, and the setup redirect
// proves nothing about the GitHub side, so a claim must never transfer.
func TestClaimInstallationOwnerRefusesToTransfer(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	ownerID, _, _ := store.UpsertUser(ctx, 555, "en")
	attackerID, _, _ := store.UpsertUser(ctx, 999, "en")
	require.NoError(t, store.ClaimInstallationOwner(ctx, 7, "acme", "Organization", ownerID))

	err := store.ClaimInstallationOwner(ctx, 7, "acme", "Organization", attackerID)
	require.ErrorIs(t, err, storage.ErrInstallationOwned)

	stolen, err := store.InstallationsForUser(ctx, attackerID)
	require.NoError(t, err)
	require.Empty(t, stolen)

	kept, err := store.InstallationsForUser(ctx, ownerID)
	require.NoError(t, err)
	require.Len(t, kept, 1)
}

func TestClaimInstallationOwnerIsIdempotentForTheOwner(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	require.NoError(t, store.ClaimInstallationOwner(ctx, 7, "acme", "Organization", userID))
	require.NoError(t, store.ClaimInstallationOwner(ctx, 7, "acme-renamed", "Organization", userID))

	found, _ := store.InstallationsForUser(ctx, userID)
	require.Len(t, found, 1)
	require.Equal(t, "acme-renamed", found[0].AccountLogin)
}

func TestInstallStateIsSingleUse(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	token, err := store.NewInstallState(ctx, userID, time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.NotEqual(t, strconv.FormatInt(userID, 10), token)

	got, err := store.TakeInstallState(ctx, token)
	require.NoError(t, err)
	require.Equal(t, userID, got)

	_, err = store.TakeInstallState(ctx, token)
	require.Error(t, err, "a spent token must not work twice")
}

func TestInstallStateExpires(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	token, err := store.NewInstallState(ctx, userID, -time.Minute)
	require.NoError(t, err)

	_, err = store.TakeInstallState(ctx, token)
	require.Error(t, err)
}

func TestTakeInstallStateRejectsUnknownToken(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	_, err := store.TakeInstallState(ctx, "not-a-token")
	require.Error(t, err)
}
