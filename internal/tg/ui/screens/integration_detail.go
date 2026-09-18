package screens

import (
	"context"
	"fmt"
	"strconv"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type integrationDetail struct {
	store Store
	roles Roles
	loc   *i18n.Bundle
}

func NewIntegrationDetail(store Store, roles Roles, loc *i18n.Bundle) ui.Screen {
	return integrationDetail{store: store, roles: roles, loc: loc}
}

func (i integrationDetail) Name() string { return "integration_detail" }

// The heavy data lives in the connect flow's params; this screen is pure
// navigation around one integration, plus the one thing params cannot be
// trusted for: whether this admin may disconnect it.
func (i integrationDetail) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	l := i.loc.Localizer(s.Lang)

	mayDisconnect, err := i.mayDisconnect(ctx, s)
	if err != nil {
		return ui.View{}, err
	}

	text := render.Emoji(render.EmojiFile, "📁") + " <b>" +
		render.Escape(s.Params["name"]) + "</b>\n\n"
	if mayDisconnect {
		text += l.T("integration_detail.hint")
	} else {
		text += l.T("integration_detail.not_owner")
	}

	rows := [][]ui.Button{
		{{
			Label:  l.T("btn.events"),
			Icon:   render.EmojiBell,
			Screen: "events",
			Params: ui.Params{
				"integration": s.Params["integration"],
				"name":        s.Params["name"],
				"chat":        s.Params["chat"],
			},
		}},
		{{
			Label:  l.T("btn.health"),
			Icon:   render.EmojiHealth,
			Screen: "health",
			Params: ui.Params{
				"integration": s.Params["integration"],
				"name":        s.Params["name"],
				"chat":        s.Params["chat"],
			},
		}},
		{{
			Label:  l.T("btn.filters"),
			Icon:   render.EmojiBlocked,
			Screen: "filters",
			Params: ui.Params{
				"integration": s.Params["integration"],
				"name":        s.Params["name"],
			},
		}},
	}

	// The guard refuses the tap anyway; drawing the button only for the admin
	// who connected the repository — or the chat's owner — keeps the refusal
	// from being the way anyone finds out.
	if mayDisconnect {
		rows = append(rows, []ui.Button{{
			Label:  l.T("btn.disconnect"),
			Icon:   render.EmojiTrash,
			Screen: "a_int_del",
			Params: ui.Params{
				"integration": s.Params["integration"],
				"chat":        s.Params["chat"],
				"name":        s.Params["name"],
			},
		}})
	}

	return ui.View{Text: text, Rows: rows}, nil
}

func (i integrationDetail) mayDisconnect(ctx context.Context, s ui.Session) (bool, error) {
	integrationID, err := strconv.ParseInt(s.Params["integration"], 10, 64)
	if err != nil {
		return false, fmt.Errorf("integration_detail: bad integration param: %w", err)
	}
	creator, err := i.store.CreatorTelegramForIntegration(ctx, integrationID)
	if err != nil {
		return false, err
	}
	if creator == s.TelegramID {
		return true, nil
	}

	telegramChatID, err := strconv.ParseInt(s.Params["chat"], 10, 64)
	if err != nil {
		return false, fmt.Errorf("integration_detail: bad chat param: %w", err)
	}
	return i.roles.IsOwner(ctx, telegramChatID, s.TelegramID)
}
