package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// result reports the outcome of a connect attempt. The callback handler puts
// "ok" or an error code in params rather than rendering ad-hoc messages, so
// every outcome lands on the same screen with the same navigation.
type result struct{}

func NewResult() ui.Screen { return result{} }

func (result) Name() string { return "result" }

func (result) Render(_ context.Context, s ui.Session) (ui.View, error) {
	var text string
	switch s.Params["status"] {
	case "ok":
		text = render.Emoji(render.EmojiCheck, "✅") + " <b>Готово</b>\n\n" +
			render.Escape(s.Params["name"]) + " подключён.\n" +
			"События уже идут — настроить их можно в разделе «Чаты»."
	case "not_admin":
		text = render.Emoji(render.EmojiCross, "❌") + " <b>Нужны права администратора</b>\n\n" +
			"Подключать репозитории к чату может только его администратор."
	case "duplicate":
		text = render.Emoji(render.EmojiInfo, "ℹ") + " <b>Уже подключено</b>\n\n" +
			render.Escape(s.Params["name"]) + " уже присылает события в этот чат."
	default:
		text = render.Emoji(render.EmojiCross, "❌") + " <b>Не получилось</b>\n\n" +
			"Попробуй ещё раз чуть позже."
	}

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{{{Label: "🏠 В начало", Screen: "home"}}},
	}, nil
}
