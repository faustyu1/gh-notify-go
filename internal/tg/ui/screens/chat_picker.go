package screens

import (
	"context"
	"strconv"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type chatPicker struct{ chats Chats }

func NewChatPicker(chats Chats) ui.Screen { return chatPicker{chats: chats} }

func (c chatPicker) Name() string { return "chat_picker" }

func (c chatPicker) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	list, err := c.chats.CandidateChatsForUser(ctx, s.UserID)
	if err != nil {
		return ui.View{}, err
	}

	if len(list) == 0 {
		return ui.View{
			Text: render.Emoji(render.EmojiInfo, "ℹ") +
				" Бот пока не добавлен ни в один твой чат.\n\n" +
				"Добавь его в группу, и чат появится здесь.",
			Rows: [][]ui.Button{{{Label: "➕ Добавить в чат", Screen: "add_to_chat"}}},
		}, nil
	}

	rows := make([][]ui.Button, 0, len(list))
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

	return ui.View{
		Text: render.Emoji(render.EmojiPeople, "👥") + " <b>Куда присылать</b>\n\n" +
			render.Escape(s.Params["name"]),
		Rows: rows,
	}, nil
}
