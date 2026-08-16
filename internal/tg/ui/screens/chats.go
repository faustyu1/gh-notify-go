package screens

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type chatsScreen struct{ store Store }

func NewChats(store Store) ui.Screen { return chatsScreen{store: store} }

func (c chatsScreen) Name() string { return "chats" }

func (c chatsScreen) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	list, err := c.store.ChatsForUser(ctx, s.UserID)
	if err != nil {
		return ui.View{}, err
	}

	if len(list) == 0 {
		return ui.View{
			Text: render.Emoji(render.EmojiInfo, "ℹ") +
				" Пока нет чатов с подключёнными репозиториями.",
			Rows: [][]ui.Button{{{Label: "🏢 Репозитории", Screen: "accounts"}}},
		}, nil
	}

	rows := make([][]ui.Button, 0, len(list)+1)
	for _, chat := range list {
		rows = append(rows, []ui.Button{{
			Label: fmt.Sprintf("💬 %s · %d", chat.Title, chat.IntegrationCount),
			Screen: "chat_detail",
			Params: ui.Params{"chat": fmt.Sprint(chat.TelegramChatID)},
		}})
	}
	rows = append(rows, []ui.Button{{Label: "➕ Добавить в чат", Screen: "add_to_chat"}})

	return ui.View{
		Text: render.Emoji(render.EmojiPeople, "👥") + " <b>Чаты</b>\n\n" +
			"Чат с подключёнными репозиториями можно замьютить или направить в топик.",
		Rows: rows,
	}, nil
}
