package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type integrationDetail struct {
	loc *i18n.Bundle
}

func NewIntegrationDetail(loc *i18n.Bundle) ui.Screen {
	return integrationDetail{loc: loc}
}

func (i integrationDetail) Name() string { return "integration_detail" }

// The heavy data lives in the connect flow's params; this screen is pure
// navigation around one integration.
func (i integrationDetail) Render(_ context.Context, s ui.Session) (ui.View, error) {
	l := i.loc.Localizer(s.Lang)

	text := render.Emoji(render.EmojiFile, "📁") + " <b>" +
		render.Escape(s.Params["name"]) + "</b>\n\n" +
		l.T("integration_detail.hint")

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{{
				Label:  l.T("btn.events"),
				Screen: "events",
				Params: ui.Params{
					"integration": s.Params["integration"],
					"name":        s.Params["name"],
					"chat":        s.Params["chat"],
				},
			}},
			{{
				Label:  l.T("btn.health"),
				Screen: "health",
				Params: ui.Params{
					"integration": s.Params["integration"],
					"name":        s.Params["name"],
					"chat":        s.Params["chat"],
				},
			}},
			{{
				Label:  l.T("btn.filters"),
				Screen: "filters",
				Params: ui.Params{
					"integration": s.Params["integration"],
					"name":        s.Params["name"],
				},
			}},
			{{
				Label:  l.T("btn.disconnect"),
				Screen: "a_int_del",
				Params: ui.Params{
					"integration": s.Params["integration"],
					"chat":        s.Params["chat"],
					"name":        s.Params["name"],
				},
			}},
		},
	}, nil
}
