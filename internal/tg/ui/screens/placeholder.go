package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// placeholder stands in for a screen that a later plan implements. It exists
// so every button in the shipped interface leads somewhere, and so a missing
// screen is visible to the user rather than surfacing as an engine error.
type placeholder struct {
	name  string
	title string
	loc   *i18n.Bundle
}

func NewPlaceholder(name, title string, loc *i18n.Bundle) ui.Screen {
	return placeholder{name: name, title: title, loc: loc}
}

func (p placeholder) Name() string { return p.name }

func (p placeholder) Render(_ context.Context, s ui.Session) (ui.View, error) {
	l := p.loc.Localizer(s.Lang)
	return ui.View{
		Text: render.Emoji(render.EmojiClock, "⏰") + " <b>" +
			render.Escape(p.title) + "</b>\n\n" + l.T("placeholder.body"),
		Rows: [][]ui.Button{{{Label: l.T("btn.home"), Screen: "home"}}},
	}, nil
}
