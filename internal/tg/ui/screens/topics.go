package screens

import (
	"context"
	"fmt"
	"strconv"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// topics picks where a forum chat's notifications land. Telegram offers no
// way to list a forum's topics, so the buttons come from what the bot has
// seen: topics created while it was in the chat, and topics somebody has
// posted in since. A topic it has never seen can still be reached — the
// "new topic" button makes one the bot is certain about.
type topics struct {
	store Store
	loc   *i18n.Bundle
}

func NewTopics(store Store, loc *i18n.Bundle) ui.Screen {
	return topics{store: store, loc: loc}
}

func (t topics) Name() string { return "topics" }

func (t topics) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	telegramChatID, err := strconv.ParseInt(s.Params["chat"], 10, 64)
	if err != nil {
		return ui.View{}, fmt.Errorf("topics: bad chat param: %w", err)
	}

	chat, err := t.store.ChatByTelegramID(ctx, telegramChatID)
	if err != nil {
		return ui.View{}, err
	}
	known, err := t.store.TopicsForChat(ctx, chat.ID)
	if err != nil {
		return ui.View{}, err
	}
	l := t.loc.Localizer(s.Lang)

	var b fmtBuilder
	b.line(render.Emoji(render.EmojiTag, "🏷") + " <b>" + l.T("topics.title") + "</b>")
	b.line("")
	if len(known) == 0 {
		b.line(l.T("topics.empty"))
	} else {
		b.line(l.T("topics.hint"))
	}

	// The General topic is always reachable, so it is a button rather than a
	// discovery: clearing the topic is how a chat goes back to it.
	rows := [][]ui.Button{{{
		Label:  l.T("btn.topic_general"),
		Icon:   mark(chat.TopicID == nil, render.EmojiChat),
		Screen: "a_topic",
		Params: ui.Params{"chat": s.Params["chat"], "topic": "0"},
	}}}

	for _, topic := range known {
		id := strconv.FormatInt(topic.TopicID, 10)
		label := topic.Title
		if label == "" {
			label = l.T("topics.unnamed", "id", id)
		}
		selected := chat.TopicID != nil && *chat.TopicID == topic.TopicID
		rows = append(rows, []ui.Button{{
			Label:  label,
			Icon:   mark(selected, render.EmojiTag),
			Screen: "a_topic",
			Params: ui.Params{"chat": s.Params["chat"], "topic": id},
		}})
	}

	rows = append(rows, []ui.Button{{
		Label:  l.T("btn.topic_new"),
		Icon:   render.EmojiPlus,
		Screen: "a_topic_new",
		Params: ui.Params{"chat": s.Params["chat"]},
	}})

	return ui.View{Text: b.String(), Rows: rows}, nil
}

// mark swaps a row's icon for a checkmark when that destination is the one
// currently in effect, so the screen shows the current setting without a
// separate line of text.
func mark(selected bool, icon string) string {
	if selected {
		return render.EmojiCheck
	}
	return icon
}
