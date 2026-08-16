package screens

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type home struct{ store Store }

func NewHome(store Store) ui.Screen { return home{store: store} }

func (h home) Name() string { return "home" }

func (h home) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	accounts, repos, chats, err := h.store.CountsForUser(ctx, s.UserID)
	if err != nil {
		return ui.View{}, err
	}

	// A user with nothing connected gets one obvious next step instead of a
	// menu of screens that would all be empty.
	if accounts == 0 {
		return ui.View{
			Text: render.Emoji(render.EmojiBot, "🤖") + " <b>GitHub Notify</b>\n\n" +
				"Подключи GitHub, и события репозиториев будут приходить в твои чаты.",
			Rows: [][]ui.Button{
				{{Label: "🔗 Подключить GitHub", Screen: "install"}},
			},
		}, nil
	}

	text := fmt.Sprintf(
		"%s <b>GitHub Notify</b>\n\n%s Аккаунтов: <b>%d</b>\n%s Репозиториев: <b>%d</b>\n%s Чатов: <b>%d</b>",
		render.Emoji(render.EmojiBot, "🤖"),
		render.Emoji(render.EmojiProfile, "👤"), accounts,
		render.Emoji(render.EmojiFile, "📁"), repos,
		render.Emoji(render.EmojiPeople, "👥"), chats,
	)

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{
				{Label: "🏢 Репозитории", Screen: "accounts"},
				{Label: "💬 Чаты", Screen: "chats"},
			},
			{
				{Label: "📊 Статус", Screen: "status"},
				{Label: "⚙️ Настройки", Screen: "settings"},
			},
			{{Label: "➕ Добавить в чат", Screen: "add_to_chat"}},
		},
	}, nil
}
