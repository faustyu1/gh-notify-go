package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type integrationDetail struct{}

func NewIntegrationDetail() ui.Screen { return integrationDetail{} }

func (i integrationDetail) Name() string { return "integration_detail" }

// The heavy data lives in the connect flow's params; this screen is pure
// navigation around one integration.
func (i integrationDetail) Render(_ context.Context, s ui.Session) (ui.View, error) {
	text := render.Emoji(render.EmojiFile, "📁") + " <b>" +
		render.Escape(s.Params["name"]) + "</b>\n\n" +
		"Что настроить для этого репозитория?"

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{{
				Label: "🔔 События",
				Screen: "events",
				Params: ui.Params{
					"integration": s.Params["integration"],
					"name":        s.Params["name"],
					"chat":        s.Params["chat"],
				},
			}},
			{{
				Label: "🚫 Фильтры",
				Screen: "filters",
				Params: ui.Params{
					"integration": s.Params["integration"],
					"name":        s.Params["name"],
				},
			}},
			{{
				Label: "🗑 Отключить",
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
