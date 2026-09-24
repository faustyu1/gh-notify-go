package screens

import (
	"context"
	"strconv"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type home struct {
	store   Store
	loc     *i18n.Bundle
	isAdmin func(telegramID int64) bool
}

// NewHome builds the root screen. isAdmin decides who gets the admin panel
// button; nil means nobody.
func NewHome(store Store, loc *i18n.Bundle, isAdmin func(telegramID int64) bool) ui.Screen {
	return home{store: store, loc: loc, isAdmin: isAdmin}
}

// withAdmin appends the admin panel row for the bot's owners.
func (h home) withAdmin(view ui.View, l *i18n.Localizer, telegramID int64) ui.View {
	if h.isAdmin != nil && h.isAdmin(telegramID) {
		view.Rows = append(view.Rows, []ui.Button{{Label: l.T("btn.admin"),
			Icon: render.EmojiLockClosed, Screen: "adm_home"}})
	}
	return view
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
		return h.withAdmin(ui.View{
			Text: render.Emoji(render.EmojiBot, "🤖") + " <b>GitHub Notify</b>\n\n" +
				l.T("home.greeting"),
			Rows: [][]ui.Button{
				{{Label: l.T("btn.connect_github"), Icon: render.EmojiLink, Screen: "install"}},
			},
		}, l, s.TelegramID), nil
	}

	text := render.Emoji(render.EmojiBot, "🤖") + " <b>GitHub Notify</b>\n\n" +
		render.Emoji(render.EmojiProfile, "👤") + " " + l.T("home.accounts") +
		": <b>" + strconv.Itoa(accounts) + "</b>\n" +
		render.Emoji(render.EmojiFile, "📁") + " " + l.T("home.repos") +
		": <b>" + strconv.Itoa(repos) + "</b>\n" +
		render.Emoji(render.EmojiPeople, "👥") + " " + l.T("home.chats") +
		": <b>" + strconv.Itoa(chats) + "</b>"

	return h.withAdmin(ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{
				{Label: l.T("btn.repos"), Icon: render.EmojiOffice, Screen: "accounts"},
				{Label: l.T("btn.chats"), Icon: render.EmojiChat, Screen: "chats"},
			},
			{
				{Label: l.T("btn.status"), Icon: render.EmojiStats, Screen: "status"},
				{Label: l.T("btn.settings"), Icon: render.EmojiSettings, Screen: "settings"},
			},
			{{Label: l.T("btn.add_to_chat"), Icon: render.EmojiPlus, Screen: "add_to_chat"}},
		},
	}, l, s.TelegramID), nil
}
