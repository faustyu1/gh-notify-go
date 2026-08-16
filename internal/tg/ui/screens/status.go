package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type statusScreen struct {
	store Store
	loc   *i18n.Bundle
}

func NewStatus(store Store, loc *i18n.Bundle) ui.Screen {
	return statusScreen{store: store, loc: loc}
}

func (s statusScreen) Name() string { return "status" }

func (s statusScreen) Render(ctx context.Context, sess ui.Session) (ui.View, error) {
	accounts, repos, chats, err := s.store.CountsForUser(ctx, sess.UserID)
	if err != nil {
		return ui.View{}, err
	}
	sent, failed, err := s.store.StatusStats(ctx, sess.UserID)
	if err != nil {
		return ui.View{}, err
	}
	l := s.loc.Localizer(sess.Lang)

	text := render.Emoji(render.EmojiStats, "📊") + " <b>" + l.T("status.title") + "</b>\n\n" +
		render.Emoji(render.EmojiProfile, "👤") + " " + l.T("status.summary",
		"accounts", accounts, "repos", repos, "chats", chats) + "\n" +
		render.Emoji(render.EmojiCheck, "✅") + " " + l.T("status.delivered24h", "n", sent)
	if failed > 0 {
		text += "\n" + render.Emoji(render.EmojiCross, "❌") + " " +
			l.T("status.failed24h", "n", failed)
	}

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{
				{Label: l.T("btn.chats"), Screen: "chats"},
				{Label: l.T("btn.repos"), Screen: "accounts"},
			},
		},
	}, nil
}
