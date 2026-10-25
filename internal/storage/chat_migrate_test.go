package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrateChatMovesTheTelegramID(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "group", false)
	installID := mustInstallation(t, store, 7, "acme", "Organization", userID)
	_, err := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	require.NoError(t, err)

	require.NoError(t, store.MigrateChat(ctx, -100, -1001))

	chat, err := store.ChatByTelegramID(ctx, -1001)
	require.NoError(t, err)
	require.Equal(t, chatID, chat.ID)

	integrations, err := store.IntegrationsInChat(ctx, chatID)
	require.NoError(t, err)
	require.Len(t, integrations, 1)

	_, err = store.ChatByTelegramID(ctx, -100)
	require.Error(t, err)
}

func TestMigrateChatFoldsTheSupergroupDuplicate(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	otherID, _, _ := store.UpsertUser(ctx, 999, "en")
	installID := mustInstallation(t, store, 7, "acme", "Organization", userID)

	oldChat, _ := store.UpsertChat(ctx, -100, "Team", "group", false)
	require.NoError(t, store.AddChatManager(ctx, oldChat, userID))
	_, err := store.CreateIntegration(ctx, oldChat, installID, 42, "acme/app", userID)
	require.NoError(t, err)

	// The bot already saw the supergroup and gave it a row of its own.
	newChat, _ := store.UpsertChat(ctx, -1001, "Team (forum)", "supergroup", true)
	require.NoError(t, store.AddChatManager(ctx, newChat, userID))
	require.NoError(t, store.AddChatManager(ctx, newChat, otherID))
	_, err = store.CreateIntegration(ctx, newChat, installID, 42, "acme/app", userID)
	require.NoError(t, err)
	_, err = store.CreateIntegration(ctx, newChat, installID, 43, "acme/lib", userID)
	require.NoError(t, err)

	require.NoError(t, store.MigrateChat(ctx, -100, -1001))

	chat, err := store.ChatByTelegramID(ctx, -1001)
	require.NoError(t, err)
	require.Equal(t, oldChat, chat.ID)
	require.Equal(t, "Team (forum)", chat.Title)

	integrations, err := store.IntegrationsInChat(ctx, oldChat)
	require.NoError(t, err)
	require.Len(t, integrations, 2)

	candidates, err := store.CandidateChatsForUser(ctx, otherID)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
}

func TestMigrateChatIgnoresUnknownChat(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	require.NoError(t, store.MigrateChat(ctx, -100, -1001))
}
