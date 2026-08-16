package screens

import (
	"context"
	"strconv"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type home struct {
	store Store
	loc   *i18n.Bundle
}

func NewHome(store Store, loc *i18n.Bundle) ui.Screen {
	return home{store: store, loc: loc}
}

func (h home) Name() string { return "home" }

func (h home) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	accounts, repos, chats, err := h.store.CountsForUser(ctx, s.UserID)
	if err != nil {
		return ui.View{}, err
	}
	l := h.loc.Localizer(s.Lang)

	// A user with nothing connected gets one obvious next step instead of a
	// menu of screens that would all be empty.
	if accounts == 0 {
		return ui.View{
			Text: render.Emoji(render.EmojiBot, "🤖") + " <b>GitHub Notify</b>\n\n" +
				l.T("home.greeting"),
			Rows: [][]ui.Button{
				{{Label: l.T("btn.connect_github"), Screen: "install"}},
			},
		}, nil
	}

	text := render.Emoji(render.EmojiBot, "🤖") + " <b>GitHub Notify</b>\n\n" +
		render.Emoji(render.EmojiProfile, "👤") + " " + l.T("home.accounts") +
		": <b>" + strconv.Itoa(accounts) + "</b>\n" +
		render.Emoji(render.EmojiFile, "📁") + " " + l.T("home.repos") +
		": <b>" + strconv.Itoa(repos) + "</b>\n" +
		render.Emoji(render.EmojiPeople, "👥") + " " + l.T("home.chats") +
		": <b>" + strconv.Itoa(chats) + "</b>"

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{
				{Label: l.T("btn.repos"), Screen: "accounts"},
				{Label: l.T("btn.chats"), Screen: "chats"},
			},
			{
				{Label: l.T("btn.status"), Screen: "status"},
				{Label: l.T("btn.settings"), Screen: "settings"},
			},
			{{Label: l.T("btn.add_to_chat"), Screen: "add_to_chat"}},
		},
	}, nil
}
