package screens

import (
	"context"
	"strconv"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type chatsScreen struct {
	store Store
	loc   *i18n.Bundle
}

func NewChats(store Store, loc *i18n.Bundle) ui.Screen {
	return chatsScreen{store: store, loc: loc}
}

func (c chatsScreen) Name() string { return "chats" }

func (c chatsScreen) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	list, err := c.store.ChatsForUser(ctx, s.UserID)
	if err != nil {
		return ui.View{}, err
	}
	l := c.loc.Localizer(s.Lang)

	if len(list) == 0 {
		return ui.View{
			Text: render.Emoji(render.EmojiInfo, "ℹ") + " " + l.T("chats.empty"),
			Rows: [][]ui.Button{{{Label: l.T("btn.repos"), Screen: "accounts"}}},
		}, nil
	}

	rows := make([][]ui.Button, 0, len(list)+1)
	for _, chat := range list {
		rows = append(rows, []ui.Button{{
			Label: l.T("chats.entry",
				"title", chat.Title, "n", chat.IntegrationCount),
			Screen: "chat_detail",
			Params: ui.Params{"chat": strconv.FormatInt(chat.TelegramChatID, 10)},
		}})
	}
	rows = append(rows, []ui.Button{{Label: l.T("btn.add_to_chat"), Screen: "add_to_chat"}})

	return ui.View{
		Text: render.Emoji(render.EmojiPeople, "👥") + " <b>" + l.T("chats.title") +
			"</b>\n\n" + l.T("chats.hint"),
		Rows: rows,
	}, nil
}
