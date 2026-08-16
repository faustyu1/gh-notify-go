package screens

import (
	"context"
	"strconv"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type chatPicker struct {
	chats Chats
	loc   *i18n.Bundle
}

func NewChatPicker(chats Chats, loc *i18n.Bundle) ui.Screen {
	return chatPicker{chats: chats, loc: loc}
}

func (c chatPicker) Name() string { return "chat_picker" }

func (c chatPicker) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	l := c.loc.Localizer(s.Lang)

	list, err := c.chats.CandidateChatsForUser(ctx, s.UserID)
	if err != nil {
		return ui.View{}, err
	}

	if len(list) == 0 {
		return ui.View{
			Text: render.Emoji(render.EmojiInfo, "ℹ") + " " + l.T("chat_picker.empty"),
			Rows: [][]ui.Button{{{Label: l.T("btn.add_to_chat"), Screen: "add_to_chat"}}},
		}, nil
	}

	rows := make([][]ui.Button, 0, len(list)+1)
	for _, chat := range list {
		// Carry the repository params forward so the connect action has
		// everything it needs without a second lookup.
		params := ui.Params{
			"installation": s.Params["installation"],
			"repo":         s.Params["repo"],
			"name":         s.Params["name"],
			"chat":         strconv.FormatInt(chat.ChatID, 10),
		}
		rows = append(rows, []ui.Button{{
			Label: "💬 " + chat.Title, Screen: "connect", Params: params,
		}})
	}

	// The way into a group the bot is not in yet has to be here too, not only
	// on the empty screen: a user with one connected chat who wants a second
	// one would otherwise have no route to it.
	rows = append(rows, []ui.Button{{
		Label: l.T("btn.add_to_chat"), Screen: "add_to_chat",
	}})

	return ui.View{
		Text: render.Emoji(render.EmojiPeople, "👥") + " <b>" + l.T("chat_picker.title") +
			"</b>\n\n" + render.Escape(s.Params["name"]),
		Rows: rows,
	}, nil
}
