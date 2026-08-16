package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
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
	userID, _ := store.UpsertUser(ctx, 555)
	found, err := store.InstallationsForUser(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestRegisterInstallationKeepsExistingOwner(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	_, err := store.UpsertInstallation(ctx, 154234486, "faustyu1", "User", userID)
	require.NoError(t, err)

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

	userID, _ := store.UpsertUser(ctx, 555)
	_, _ = store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)

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

	userID, _ := store.UpsertUser(ctx, 555)
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	installID, _ := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	_, _ = store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)

	require.NoError(t, store.DeleteInstallation(ctx, 7))

	var count int
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM integrations`).Scan(&count))
	require.Zero(t, count, "uninstalling the App must remove its integrations")
}
