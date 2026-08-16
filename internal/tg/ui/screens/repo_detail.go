package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type repoDetail struct {
	store Store
	loc   *i18n.Bundle
}

func NewRepoDetail(store Store, loc *i18n.Bundle) ui.Screen {
	return repoDetail{store: store, loc: loc}
}

func (r repoDetail) Name() string { return "repo_detail" }

func (r repoDetail) Render(_ context.Context, s ui.Session) (ui.View, error) {
	l := r.loc.Localizer(s.Lang)
	name := s.Params["name"]

	text := render.Emoji(render.EmojiFile, "📁") + " <b>" + render.Escape(name) + "</b>\n\n" +
		l.T("repo_detail.hint")

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{{
				Label:  l.T("btn.connect_to_chat"),
				Screen: "chat_picker",
				Params: s.Params,
			}},
			{{
				Label: l.T("btn.open_github"),
				URL:   "https://github.com/" + name,
			}},
		},
	}, nil
}
