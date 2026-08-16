package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// result reports the outcome of a connect attempt. The callback handler puts
// "ok" or an error code in params rather than rendering ad-hoc messages, so
// every outcome lands on the same screen with the same navigation.
type result struct {
	loc *i18n.Bundle
}

func NewResult(loc *i18n.Bundle) ui.Screen { return result{loc: loc} }

func (result) Name() string { return "result" }

func (r result) Render(_ context.Context, s ui.Session) (ui.View, error) {
	l := r.loc.Localizer(s.Lang)

	var emoji, text string
	switch s.Params["status"] {
	case "ok":
		emoji = render.Emoji(render.EmojiCheck, "✅")
		text = l.T("result.ok", "name", render.Escape(s.Params["name"]))
	case "not_admin":
		emoji = render.Emoji(render.EmojiCross, "❌")
		text = l.T("result.not_admin")
	case "duplicate":
		emoji = render.Emoji(render.EmojiInfo, "ℹ")
		text = l.T("result.duplicate", "name", render.Escape(s.Params["name"]))
	default:
		emoji = render.Emoji(render.EmojiCross, "❌")
		text = l.T("result.error")
	}

	return ui.View{
		Text: emoji + " " + text,
		Rows: [][]ui.Button{{{Label: l.T("btn.home"), Screen: "home"}}},
	}, nil
}
