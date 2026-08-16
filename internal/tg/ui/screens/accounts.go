package screens

import (
	"context"
	"strconv"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type accounts struct{ store Store }

func NewAccounts(store Store) ui.Screen { return accounts{store: store} }

func (a accounts) Name() string { return "accounts" }

func (a accounts) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	installations, err := a.store.InstallationsForUser(ctx, s.UserID)
	if err != nil {
		return ui.View{}, err
	}

	if len(installations) == 0 {
		return ui.View{
			Text: render.Emoji(render.EmojiInfo, "ℹ") +
				" Пока нет подключённых аккаунтов GitHub.",
			Rows: [][]ui.Button{{{Label: "🔗 Подключить GitHub", Screen: "install"}}},
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
	rows = append(rows, []ui.Button{{Label: "➕ Ещё аккаунт", Screen: "install"}})

	return ui.View{
		Text: render.Emoji(render.EmojiProfile, "👤") + " <b>Аккаунты GitHub</b>\n\n" +
			"Выбери, где лежит репозиторий.",
		Rows: rows,
	}, nil
}
