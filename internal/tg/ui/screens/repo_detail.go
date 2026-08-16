package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type repoDetail struct{ store Store }

func NewRepoDetail(store Store) ui.Screen { return repoDetail{store: store} }

func (r repoDetail) Name() string { return "repo_detail" }

func (r repoDetail) Render(_ context.Context, s ui.Session) (ui.View, error) {
	name := s.Params["name"]

	text := render.Emoji(render.EmojiFile, "📁") + " <b>" + render.Escape(name) + "</b>\n\n" +
		"Выбери чат, куда присылать события этого репозитория."

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{{
				Label:  "💬 Подключить к чату",
				Screen: "chat_picker",
				Params: s.Params,
			}},
			{{
				Label: "🔗 Открыть на GitHub",
				URL:   "https://github.com/" + name,
			}},
		},
	}, nil
}
