package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// placeholder stands in for a screen that a later plan implements. It exists
// so every button in the shipped interface leads somewhere, and so a missing
// screen is visible to the user rather than surfacing as an engine error.
type placeholder struct {
	name  string
	title string
}

func NewPlaceholder(name, title string) ui.Screen {
	return placeholder{name: name, title: title}
}

func (p placeholder) Name() string { return p.name }

func (p placeholder) Render(_ context.Context, _ ui.Session) (ui.View, error) {
	return ui.View{
		Text: render.Emoji(render.EmojiClock, "⏰") + " <b>" +
			render.Escape(p.title) + "</b>\n\nЭтот раздел ещё в работе.",
		Rows: [][]ui.Button{{{Label: "🏠 В начало", Screen: "home"}}},
	}, nil
}
