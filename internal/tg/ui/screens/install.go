package screens

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type install struct {
	slug      string
	publicURL string
}

func NewInstall(slug, publicURL string) ui.Screen {
	return install{slug: slug, publicURL: publicURL}
}

func (i install) Name() string { return "install" }

func (i install) Render(_ context.Context, s ui.Session) (ui.View, error) {
	// state carries the Telegram user id back through GitHub's redirect, so
	// the setup callback knows whose installation this is.
	url := fmt.Sprintf("https://github.com/apps/%s/installations/new?state=%d",
		i.slug, s.UserID)

	text := render.Emoji(render.EmojiLink, "🔗") + " <b>Подключение GitHub</b>\n\n" +
		"Нажми кнопку ниже, выбери аккаунт или организацию и отметь нужные репозитории. " +
		"После установки GitHub вернёт тебя обратно сюда."

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{{Label: "🔗 Установить GitHub App", URL: url}},
			{{Label: "🔄 Я установил", Screen: "accounts"}},
		},
	}, nil
}
