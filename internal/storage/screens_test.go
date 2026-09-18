package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallationsForUserReturnsOwnedAccounts(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	otherID, _, _ := store.UpsertUser(ctx, 999, "en")
	mustInstallation(t, store, 7, "acme", "Organization", userID)
	mustInstallation(t, store, 8, "someone-else", "User", otherID)

	found, err := store.InstallationsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "acme", found[0].AccountLogin)
	require.False(t, found[0].Suspended)
}

func TestChatsForUserCountsIntegrations(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
	installID := mustInstallation(t, store, 7, "acme", "Organization", userID)
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

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	chatID, _ := store.UpsertChat(ctx, -100, "Fresh group", "supergroup", false)
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

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	otherID, _, _ := store.UpsertUser(ctx, 999, "en")
	chatID, _ := store.UpsertChat(ctx, -200, "Not yours", "supergroup", false)
	require.NoError(t, store.AddChatManager(ctx, chatID, otherID))

	candidates, err := store.CandidateChatsForUser(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, candidates)
}

func TestAddChatManagerIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup", false)

	require.NoError(t, store.AddChatManager(ctx, chatID, userID))
	require.NoError(t, store.AddChatManager(ctx, chatID, userID))

	candidates, err := store.CandidateChatsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
}

func TestCountsForUserSummarisesEverything(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
	installID := mustInstallation(t, store, 7, "acme", "Organization", userID)
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

	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
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

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
	installID := mustInstallation(t, store, 7, "acme", "Organization", userID)
	integrationID, _ := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)

	require.NoError(t, store.MarkIntegrationBroken(ctx, integrationID, "bot was kicked"))

	found, err := store.IntegrationsForRepo(ctx, 42, 7)
	require.NoError(t, err)
	require.Empty(t, found)
}

// Authorization resolves the chat from the row itself, so these two lookups
// are what stands between a callback param and someone else's chat.
func TestTelegramChatForIntegrationAndFilter(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
	installID := mustInstallation(t, store, 7, "acme", "Organization", userID)
	integrationID, _ := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	filterID, err := store.AddFilter(ctx, integrationID, "author", "dependabot")
	require.NoError(t, err)

	telegramChatID, err := store.TelegramChatForIntegration(ctx, integrationID)
	require.NoError(t, err)
	require.Equal(t, int64(-100), telegramChatID)

	telegramChatID, err = store.TelegramChatForFilter(ctx, filterID)
	require.NoError(t, err)
	require.Equal(t, int64(-100), telegramChatID)
}

func TestTelegramChatLookupsFailOnUnknownIDs(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	_, err := store.TelegramChatForIntegration(ctx, 4242)
	require.Error(t, err)

	_, err = store.TelegramChatForFilter(ctx, 4242)
	require.Error(t, err)
}

// Being removed from a chat deletes it and everything wired to it, so the
// picker and the chats screen stop offering a chat the bot is no longer in.
func TestDeleteChatCascades(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
	installID := mustInstallation(t, store, 7, "acme", "Organization", userID)
	_, err := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	require.NoError(t, err)
	require.NoError(t, store.AddChatManager(ctx, chatID, userID))
	require.NoError(t, store.RecordChatTopic(ctx, -100, 5, "Releases"))

	require.NoError(t, store.DeleteChat(ctx, -100))

	_, err = store.ChatByTelegramID(ctx, -100)
	require.Error(t, err)

	list, err := store.IntegrationsInChat(ctx, chatID)
	require.NoError(t, err)
	require.Empty(t, list)

	topics, err := store.TopicsForChat(ctx, chatID)
	require.NoError(t, err)
	require.Empty(t, topics)
}
