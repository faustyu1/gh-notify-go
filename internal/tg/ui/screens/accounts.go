package screens

import (
	"context"
	"strconv"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type accounts struct {
	store Store
	loc   *i18n.Bundle
}

func NewAccounts(store Store, loc *i18n.Bundle) ui.Screen {
	return accounts{store: store, loc: loc}
}

func (a accounts) Name() string { return "accounts" }

func (a accounts) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	installations, err := a.store.InstallationsForUser(ctx, s.UserID)
	if err != nil {
		return ui.View{}, err
	}
	l := a.loc.Localizer(s.Lang)

	if len(installations) == 0 {
		return ui.View{
			Text: render.Emoji(render.EmojiInfo, "ℹ") + " " + l.T("accounts.empty"),
			Rows: [][]ui.Button{{{Label: l.T("btn.connect_github"), Screen: "install"}}},
		}, nil
	}

	rows := make([][]ui.Button, 0, len(installations)+1)
	for _, it := range installations {
		icon := "🏢"
		if it.AccountType == "User" {
			icon = "👤"
		}
		// A suspended installation cannot mint tokens, so it is labelled
		// rather than offered as if it worked.
		if it.Suspended {
			icon = "⚠️"
		}
		rows = append(rows, []ui.Button{{
			Label:  icon + " " + it.AccountLogin,
			Screen: "repos",
			Params: ui.Params{"installation": strconv.FormatInt(it.ID, 10)},
		}})
	}
	rows = append(rows, []ui.Button{{Label: l.T("btn.more_accounts"), Screen: "install"}})

	return ui.View{
		Text: render.Emoji(render.EmojiProfile, "👤") + " <b>" + l.T("accounts.title") +
			"</b>\n\n" + l.T("accounts.hint"),
		Rows: rows,
	}, nil
}
