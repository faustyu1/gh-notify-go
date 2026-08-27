package tg

import (
	"log/slog"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// recordTopic is the whole of the bot's interest in group traffic. Telegram
// exposes no method for listing a forum's topics, so the picker is built from
// what passes by: the service message a new topic starts with, a rename, and
// any message posted inside a topic — which is the only way a topic older
// than the bot ever becomes known.
//
// A closed topic is forgotten instead: messages to it fail, so offering it
// would only produce a broken delivery later.
func recordTopic(ctx *th.Context, deps HandlerDeps, message telego.Message) error {
	topicID := int64(message.MessageThreadID)
	if topicID == 0 {
		return nil
	}

	switch {
	case message.ForumTopicClosed != nil:
		return deps.Store.ForgetChatTopic(ctx, message.Chat.ID, topicID)
	case message.ForumTopicCreated != nil:
		return deps.Store.RecordChatTopic(ctx, message.Chat.ID, topicID,
			message.ForumTopicCreated.Name)
	case message.ForumTopicEdited != nil:
		// An edit that only changed the icon carries no name; an empty one
		// leaves the stored title alone.
		return deps.Store.RecordChatTopic(ctx, message.Chat.ID, topicID,
			message.ForumTopicEdited.Name)
	case message.ForumTopicReopened != nil:
		return deps.Store.RecordChatTopic(ctx, message.Chat.ID, topicID, "")
	case message.IsTopicMessage:
		// A message inside a topic usually quotes the topic's own creation
		// message, which is where the name of a pre-existing topic comes from.
		name := ""
		if message.ReplyToMessage != nil && message.ReplyToMessage.ForumTopicCreated != nil {
			name = message.ReplyToMessage.ForumTopicCreated.Name
		}
		return deps.Store.RecordChatTopic(ctx, message.Chat.ID, topicID, name)
	}
	return nil
}

// applyTopic points a chat's deliveries at a topic the picker offered. Topic
// 0 is the General topic, which is stored as no topic at all.
func applyTopic(
	ctx *th.Context, deps HandlerDeps, userID, telegramID int64,
	lang string, params ui.Params,
) error {
	telegramChatID := paramInt(params["chat"])
	topicID := paramInt(params["topic"])

	var topic *int64
	if topicID > 0 {
		topic = &topicID
	}
	if err := deps.Store.SetChatTopic(ctx, telegramChatID, topic); err != nil {
		return err
	}
	audit(ctx, deps, userID, telegramChatID, "chat.topic",
		map[string]any{"topic": topicID})
	return reopen(ctx, deps, userID, telegramID, lang, "chat_detail", params)
}

// createTopic makes a topic the bot is certain of, for the chat where nothing
// it can see exists yet: a forum whose topics all predate it, or one that has
// no topic worth reusing. It needs the can_manage_topics right, so a failure
// here is reported rather than swallowed.
func createTopic(
	ctx *th.Context, deps HandlerDeps, userID, telegramID int64,
	lang string, params ui.Params,
) error {
	telegramChatID := paramInt(params["chat"])
	name := deps.Loc.Localizer(lang).T("topics.new_name")

	topic, err := ctx.Bot().CreateForumTopic(ctx, &telego.CreateForumTopicParams{
		ChatID: telego.ChatID{ID: telegramChatID},
		Name:   name,
	})
	if err != nil {
		slog.Warn("create forum topic", "chat", telegramChatID, "error", err)
		return deny(ctx, deps, userID, telegramID, lang, "topic_failed")
	}

	topicID := int64(topic.MessageThreadID)
	if err := deps.Store.RecordChatTopic(ctx, telegramChatID, topicID, topic.Name); err != nil {
		return err
	}
	if err := deps.Store.SetChatTopic(ctx, telegramChatID, &topicID); err != nil {
		return err
	}
	audit(ctx, deps, userID, telegramChatID, "chat.topic_create",
		map[string]any{"topic": topicID, "name": topic.Name})
	return reopen(ctx, deps, userID, telegramID, lang, "chat_detail", params)
}
