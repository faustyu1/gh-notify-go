package screens

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// addToChat explains how the bot gets into a group. The heavy lifting is a
// Telegram deep link: startgroup opens the client's own group picker, and the
// my_chat_member handler records the chat and greets it once the bot lands
// there.
type addToChat struct{ botUser string }

func NewAddToChat(botUser string) ui.Screen { return addToChat{botUser: botUser} }

func (a addToChat) Name() string { return "add_to_chat" }

func (a addToChat) Render(_ context.Context, _ ui.Session) (ui.View, error) {
	text := render.Emoji(render.EmojiPeople, "👥") + " <b>Добавить в чат</b>\n\n" +
		"Нажми кнопку и выбери группу — бот появится там и сразу пришлёт кнопку настройки.\n\n" +
		"После этого группа появится в списке при подключении репозитория."

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{{
			{
				Label: "➕ Добавить в группу",
				URL:   fmt.Sprintf("https://t.me/%s?startgroup=add", a.botUser),
			},
		}},
	}, nil
}
