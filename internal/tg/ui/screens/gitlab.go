package screens

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/domain"
	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// GitLabWebhookPath is where every GitLab connection posts its events.
const GitLabWebhookPath = "/gl/webhook"

// gitlabName labels a connection: GitLab gives no account name, so it is
// named after the namespace of the first project it delivered for.
func gitlabName(l *i18n.Localizer, it domain.Installation) string {
	if it.AccountLogin == "" {
		return l.T("gitlab.unnamed")
	}
	return l.T("gitlab.projects_title", "name", it.AccountLogin)
}

type gitlabHook struct {
	publicURL string
	gitlab    GitLab
	loc       *i18n.Bundle
}

// NewGitLabHook shows the owner what to paste into GitLab's webhook form.
func NewGitLabHook(publicURL string, gitlab GitLab, loc *i18n.Bundle) ui.Screen {
	return gitlabHook{publicURL: strings.TrimRight(publicURL, "/"), gitlab: gitlab, loc: loc}
}

func (g gitlabHook) Name() string { return "gl_hook" }

func (g gitlabHook) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	l := g.loc.Localizer(s.Lang)
	installationID, err := strconv.ParseInt(s.Params["installation"], 10, 64)
	if err != nil {
		return ui.View{}, fmt.Errorf("gl_hook screen: bad installation param: %w", err)
	}

	// Scoped to the owner: the token is what lets anyone post in their name.
	token, err := g.gitlab.GitLabWebhookToken(ctx, installationID, s.UserID)
	if err != nil {
		return ui.View{}, err
	}

	params := ui.Params{"installation": s.Params["installation"]}
	return ui.View{
		Text: render.Emoji(render.EmojiCode, "🦊") + " <b>" + l.T("gitlab.hook_title") +
			"</b>\n\n" + l.T("gitlab.hook_body",
			"url", render.Escape(g.publicURL+GitLabWebhookPath),
			"token", render.Escape(token)),
		Rows: [][]ui.Button{
			{{Label: l.T("btn.gitlab_projects"), Icon: render.EmojiFile,
				Screen: "gl_projects", Params: params}},
			{{Label: l.T("btn.gitlab_delete"), Icon: render.EmojiTrash,
				Screen: "gl_delete", Params: params}},
		},
	}, nil
}

type gitlabProjects struct {
	store    Store
	gitlab   GitLab
	pageSize int
	loc      *i18n.Bundle
}

// NewGitLabProjects lists the projects a GitLab connection has delivered
// for; it is the GitLab counterpart of the repos screen.
func NewGitLabProjects(store Store, gitlab GitLab, pageSize int, loc *i18n.Bundle) ui.Screen {
	return gitlabProjects{store: store, gitlab: gitlab, pageSize: pageSize, loc: loc}
}

func (g gitlabProjects) Name() string { return "gl_projects" }

func (g gitlabProjects) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	l := g.loc.Localizer(s.Lang)
	installationID, err := strconv.ParseInt(s.Params["installation"], 10, 64)
	if err != nil {
		return ui.View{}, fmt.Errorf("gl_projects screen: bad installation param: %w", err)
	}

	installation, err := g.store.InstallationByID(ctx, installationID)
	if err != nil {
		return ui.View{}, err
	}
	list, err := g.gitlab.GitLabProjects(ctx, installationID, s.UserID)
	if err != nil {
		return ui.View{}, err
	}

	base := ui.Params{"installation": s.Params["installation"]}
	footer := [][]ui.Button{
		{
			{Label: l.T("btn.refresh"), Icon: render.EmojiRefresh, Screen: "a_refresh",
				Params: ui.Params{"screen": "gl_projects",
					"installation": s.Params["installation"], "page": s.Params["page"]}},
			{Label: l.T("btn.gitlab_webhook"), Icon: render.EmojiSettings,
				Screen: "gl_hook", Params: base},
		},
	}
	title := render.Emoji(render.EmojiCode, "🦊") + " <b>" +
		render.Escape(gitlabName(l, installation)) + "</b>\n\n"

	if len(list) == 0 {
		return ui.View{Text: title + l.T("gitlab.projects_empty"), Rows: footer}, nil
	}

	page, _ := strconv.Atoi(s.Params["page"])
	pages := (len(list) + g.pageSize - 1) / g.pageSize
	page = max(0, min(page, pages-1))
	start := page * g.pageSize
	end := min(start+g.pageSize, len(list))

	rows := make([][]ui.Button, 0, g.pageSize+3)
	for _, p := range list[start:end] {
		rows = append(rows, []ui.Button{{
			Label:  p.Path,
			Icon:   render.EmojiFolder,
			Screen: "repo_detail",
			Params: ui.Params{
				"installation": s.Params["installation"],
				"repo":         strconv.FormatInt(p.ID, 10),
				"name":         p.Path,
				"provider":     domain.ProviderGitLab,
				"url":          p.WebURL,
			},
		}})
	}

	if pages > 1 {
		var nav []ui.Button
		if page > 0 {
			nav = append(nav, ui.Button{Label: l.T("btn.prev_page"), Icon: render.EmojiBack,
				Screen: "gl_projects", Params: ui.Params{
					"installation": s.Params["installation"], "page": strconv.Itoa(page - 1)}})
		}
		if page < pages-1 {
			nav = append(nav, ui.Button{Label: l.T("btn.next_page"), Icon: render.EmojiForward,
				Screen: "gl_projects", Params: ui.Params{
					"installation": s.Params["installation"], "page": strconv.Itoa(page + 1)}})
		}
		rows = append(rows, nav)
	}

	text := title + l.T("gitlab.projects_count", "n", len(list))
	if pages > 1 {
		text += l.T("repos.page", "n", page+1, "total", pages)
	}
	return ui.View{Text: text, Rows: append(rows, footer...)}, nil
}

type gitlabDelete struct {
	loc *i18n.Bundle
}

// NewGitLabDelete asks before a connection goes: deleting it silences every
// chat its projects deliver to.
func NewGitLabDelete(loc *i18n.Bundle) ui.Screen {
	return gitlabDelete{loc: loc}
}

func (g gitlabDelete) Name() string { return "gl_delete" }

func (g gitlabDelete) Render(_ context.Context, s ui.Session) (ui.View, error) {
	l := g.loc.Localizer(s.Lang)
	return ui.View{
		Text: render.Emoji(render.EmojiWarning, "⚠️") + " " + l.T("gitlab.delete_confirm"),
		Rows: [][]ui.Button{{{
			Label: l.T("btn.gitlab_delete_yes"), Icon: render.EmojiTrash, Screen: "a_gl_del",
			Params: ui.Params{"installation": s.Params["installation"]},
		}}},
	}, nil
}
