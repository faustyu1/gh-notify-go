package screens

import (
	"context"

	"github.com/faustyu/gh-notify-go/internal/domain"
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

	// A GitLab project may live on any instance, so its address travels in
	// the params rather than being built from the name.
	open := ui.Button{Label: l.T("btn.open_github"), Icon: render.EmojiLink,
		URL: "https://github.com/" + name}
	if s.Params["provider"] == domain.ProviderGitLab {
		open = ui.Button{Label: l.T("btn.open_gitlab"), Icon: render.EmojiLink,
			URL: s.Params["url"]}
	}

	rows := [][]ui.Button{{{
		Label:  l.T("btn.connect_to_chat"),
		Icon:   render.EmojiChat,
		Screen: "chat_picker",
		Params: s.Params,
	}}}
	if open.URL != "" {
		rows = append(rows, []ui.Button{open})
	}
	return ui.View{Text: text, Rows: rows}, nil
}
