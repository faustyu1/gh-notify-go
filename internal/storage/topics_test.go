package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/storage"
)

func TestRecordChatTopicKeepsKnownName(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	chatID, err := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
	require.NoError(t, err)

	require.NoError(t, store.RecordChatTopic(ctx, -100, 5, "Releases"))
	// A plain message inside the topic proves it exists but names nothing.
	require.NoError(t, store.RecordChatTopic(ctx, -100, 5, ""))

	topics, err := store.TopicsForChat(ctx, chatID)
	require.NoError(t, err)
	require.Equal(t, []storage.ChatTopic{{TopicID: 5, Title: "Releases"}}, topics)

	// A rename does replace it.
	require.NoError(t, store.RecordChatTopic(ctx, -100, 5, "Deploys"))
	topics, err = store.TopicsForChat(ctx, chatID)
	require.NoError(t, err)
	require.Equal(t, "Deploys", topics[0].Title)
}

func TestRecordChatTopicIgnoresUnknownChat(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	require.NoError(t, store.RecordChatTopic(ctx, -999, 5, "Releases"))

	var count int
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM chat_topics`).Scan(&count))
	require.Zero(t, count)
}

func TestTopicsForChatPutsNamedTopicsFirst(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
	require.NoError(t, store.RecordChatTopic(ctx, -100, 9, ""))
	require.NoError(t, store.RecordChatTopic(ctx, -100, 4, "Releases"))

	topics, err := store.TopicsForChat(ctx, chatID)
	require.NoError(t, err)
	require.Equal(t, []storage.ChatTopic{
		{TopicID: 4, Title: "Releases"},
		{TopicID: 9, Title: ""},
	}, topics)
}

func TestForgetChatTopicDropsOnlyThatTopic(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
	require.NoError(t, store.RecordChatTopic(ctx, -100, 4, "Releases"))
	require.NoError(t, store.RecordChatTopic(ctx, -100, 5, "Support"))

	require.NoError(t, store.ForgetChatTopic(ctx, -100, 4))

	topics, err := store.TopicsForChat(ctx, chatID)
	require.NoError(t, err)
	require.Equal(t, []storage.ChatTopic{{TopicID: 5, Title: "Support"}}, topics)
}

// A topic Telegram says is gone must leave both the chat's setting and the
// picker, otherwise the next admin re-selects a topic that cannot be posted to.
func TestClearTopicForgetsTheTopicItCleared(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
	require.NoError(t, store.RecordChatTopic(ctx, -100, 4, "Releases"))
	require.NoError(t, store.RecordChatTopic(ctx, -100, 5, "Support"))
	topic := int64(4)
	require.NoError(t, store.SetChatTopic(ctx, -100, &topic))

	require.NoError(t, store.ClearTopic(ctx, -100))

	chat, err := store.ChatByTelegramID(ctx, -100)
	require.NoError(t, err)
	require.Nil(t, chat.TopicID)

	topics, err := store.TopicsForChat(ctx, chatID)
	require.NoError(t, err)
	require.Equal(t, []storage.ChatTopic{{TopicID: 5, Title: "Support"}}, topics)
}

func TestCreatorTelegramForIntegration(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
	installID := mustInstallation(t, store, 7, "acme", "Organization", userID)
	integrationID, err := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	require.NoError(t, err)

	creator, err := store.CreatorTelegramForIntegration(ctx, integrationID)
	require.NoError(t, err)
	require.Equal(t, int64(555), creator)

	_, err = store.CreatorTelegramForIntegration(ctx, integrationID+1)
	require.Error(t, err)
}
