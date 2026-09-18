package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type settingsScreen struct {
	chatPerMinute int
	loc           *i18n.Bundle
}

func NewSettings(chatPerMinute int, loc *i18n.Bundle) ui.Screen {
	return settingsScreen{chatPerMinute: chatPerMinute, loc: loc}
}

// Settings shows the few process-wide facts worth knowing. Star anti-spam is
// not configurable on purpose: one notification per user per repository, ever.
// The language row is the one thing the user owns here.
func (s settingsScreen) Name() string { return "settings" }

func (s settingsScreen) Render(_ context.Context, sess ui.Session) (ui.View, error) {
	l := s.loc.Localizer(sess.Lang)

	text := render.Emoji(render.EmojiSettings, "⚙") + " <b>" + l.T("settings.title") + "</b>\n\n" +
		render.Emoji(render.EmojiStar, "⭐") + " " + l.T("settings.stars_line") + "\n" +
		render.Emoji(render.EmojiRate, "🚦") + " " +
		l.T("settings.rate_line", "n", s.chatPerMinute)

	// One button per shipped locale, the active one marked with a dot and a
	// premium icon. The dot stays in the label on purpose: icons disappear
	// when Telegram refuses custom emoji, and which language is active is
	// information, not decoration.
	var langRow []ui.Button
	for _, lang := range s.loc.Languages() {
		label, icon := lang.Name, ""
		if lang.Code == l.Lang() {
			label, icon = "● "+lang.Name, render.EmojiLanguage
		}
		langRow = append(langRow, ui.Button{
			Label:  label,
			Icon:   icon,
			Screen: "a_user_lang",
			Params: ui.Params{"lang": lang.Code},
		})
	}

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			langRow,
			{{Label: l.T("btn.home"), Icon: render.EmojiHouse, Screen: "home"}},
		},
	}, nil
}
