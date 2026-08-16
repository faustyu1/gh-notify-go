package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallationsForUserReturnsOwnedAccounts(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	otherID, _ := store.UpsertUser(ctx, 999)
	_, err := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	require.NoError(t, err)
	_, err = store.UpsertInstallation(ctx, 8, "someone-else", "User", otherID)
	require.NoError(t, err)

	found, err := store.InstallationsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "acme", found[0].AccountLogin)
	require.False(t, found[0].Suspended)
}

func TestChatsForUserCountsIntegrations(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	installID, _ := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	_, err := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	require.NoError(t, err)
	_, err = store.CreateIntegration(ctx, chatID, installID, 43, "acme/lib", userID)
	require.NoError(t, err)

	chats, err := store.ChatsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, chats, 1)
	require.Equal(t, "Team", chats[0].Title)
	require.Equal(t, 2, chats[0].IntegrationCount)
}

func TestCandidateChatsIncludeChatsWithNoIntegrationYet(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	chatID, _ := store.UpsertChat(ctx, -100, "Fresh group", "supergroup")
	require.NoError(t, store.AddChatManager(ctx, chatID, userID))

	// ChatsForUser is integration-based and must still be empty here.
	connected, err := store.ChatsForUser(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, connected)

	// The picker must still offer the chat, or the first connect is
	// impossible.
	candidates, err := store.CandidateChatsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, "Fresh group", candidates[0].Title)
}

func TestCandidateChatsExcludeOtherPeoplesChats(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	otherID, _ := store.UpsertUser(ctx, 999)
	chatID, _ := store.UpsertChat(ctx, -200, "Not yours", "supergroup")
	require.NoError(t, store.AddChatManager(ctx, chatID, otherID))

	candidates, err := store.CandidateChatsForUser(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, candidates)
}

func TestAddChatManagerIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")

	require.NoError(t, store.AddChatManager(ctx, chatID, userID))
	require.NoError(t, store.AddChatManager(ctx, chatID, userID))

	candidates, err := store.CandidateChatsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
}

func TestCountsForUserSummarisesEverything(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	installID, _ := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	_, err := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	require.NoError(t, err)

	accounts, repos, chats, err := store.CountsForUser(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, 1, accounts)
	require.Equal(t, 1, repos)
	require.Equal(t, 1, chats)
}

func TestClearTopicRemovesTopicID(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	_, err := store.Pool().Exec(ctx,
		`UPDATE chats SET topic_id = 77 WHERE id = $1`, chatID)
	require.NoError(t, err)

	require.NoError(t, store.ClearTopic(ctx, -100))

	var topic *int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT topic_id FROM chats WHERE id = $1`, chatID).Scan(&topic))
	require.Nil(t, topic)
}

func TestMarkIntegrationBrokenExcludesItFromFanout(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _ := store.UpsertUser(ctx, 555)
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	installID, _ := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	integrationID, _ := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)

	require.NoError(t, store.MarkIntegrationBroken(ctx, integrationID, "bot was kicked"))

	found, err := store.IntegrationsForRepo(ctx, 42, 7)
	require.NoError(t, err)
	require.Empty(t, found)
}
