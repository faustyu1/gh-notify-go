package screens

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type repos struct {
	store    Store
	repos    Repos
	pageSize int
	loc      *i18n.Bundle
}

func NewRepos(store Store, source Repos, pageSize int, loc *i18n.Bundle) ui.Screen {
	return repos{store: store, repos: source, pageSize: pageSize, loc: loc}
}

func (r repos) Name() string { return "repos" }

func (r repos) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	installationID, err := strconv.ParseInt(s.Params["installation"], 10, 64)
	if err != nil {
		return ui.View{}, fmt.Errorf("repos screen: bad installation param: %w", err)
	}
	l := r.loc.Localizer(s.Lang)

	installation, err := r.store.InstallationByID(ctx, installationID)
	if err != nil {
		return ui.View{}, err
	}

	list, err := r.repos.ListRepositories(ctx, installation.GitHubInstallationID)
	if err != nil {
		return ui.View{}, err
	}

	query := strings.TrimSpace(s.Params["q"])
	base := ui.Params{"installation": s.Params["installation"]}
	if query != "" {
		base["q"] = query
	}
	withPage := func(page int) ui.Params {
		p := ui.Params{"page": strconv.Itoa(page)}
		for k, v := range base {
			p[k] = v
		}
		return p
	}

	if len(list) == 0 && query == "" {
		return ui.View{
			Text: render.Emoji(render.EmojiInfo, "ℹ") + " " + l.T("repos.empty"),
			Rows: [][]ui.Button{{{Label: l.T("btn.configure_access"),
				Icon: render.EmojiLink, Screen: "install"}}},
		}, nil
	}

	// The most recently pushed repositories are the ones being worked on, so
	// they come first; a long list is otherwise alphabetical noise.
	slices.SortStableFunc(list, func(a, b ghapp.Repository) int {
		return b.PushedAt.Compare(a.PushedAt)
	})

	if query != "" {
		needle := strings.ToLower(query)
		list = slices.DeleteFunc(list, func(repo ghapp.Repository) bool {
			return !strings.Contains(strings.ToLower(repo.FullName), needle)
		})
	}

	search := ui.Button{Label: l.T("repos.search"), Icon: render.EmojiEye,
		Screen: "a_repo_search", Params: ui.Params{"installation": s.Params["installation"]}}
	if query != "" {
		search = ui.Button{Label: l.T("repos.search_reset"), Icon: render.EmojiCross,
			Screen: "repos", Params: ui.Params{"installation": s.Params["installation"]}}
	}

	if len(list) == 0 {
		return ui.View{
			Text: render.Emoji(render.EmojiInfo, "ℹ") + " " +
				l.T("repos.search_empty", "q", render.Escape(query)),
			Rows: [][]ui.Button{{search}},
		}, nil
	}

	page, _ := strconv.Atoi(s.Params["page"])
	pages := (len(list) + r.pageSize - 1) / r.pageSize
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}

	start := page * r.pageSize
	end := min(start+r.pageSize, len(list))

	rows := make([][]ui.Button, 0, r.pageSize+2)
	rows = append(rows, []ui.Button{search})
	for _, repo := range list[start:end] {
		icon := render.EmojiFolder
		if repo.Private {
			icon = render.EmojiLockClosed
		}
		rows = append(rows, []ui.Button{{
			Label:  repo.FullName,
			Icon:   icon,
			Screen: "repo_detail",
			Params: ui.Params{
				"installation": s.Params["installation"],
				"repo":         strconv.FormatInt(repo.GitHubID, 10),
				"name":         repo.FullName,
			},
		}})
	}

	// Pagination controls appear only when there is more than one page, so a
	// short list is not cluttered with dead buttons.
	if pages > 1 {
		var nav []ui.Button
		if page > 0 {
			nav = append(nav, ui.Button{
				Label: l.T("btn.prev_page"), Icon: render.EmojiBack, Screen: "repos",
				Params: withPage(page - 1),
			})
		}
		if page < pages-1 {
			nav = append(nav, ui.Button{
				Label: l.T("btn.next_page"), Icon: render.EmojiForward, Screen: "repos",
				Params: withPage(page + 1),
			})
		}
		rows = append(rows, nav)
	}

	text := render.Emoji(render.EmojiFile, "📁") + " <b>" +
		render.Escape(installation.AccountLogin) + "</b>\n\n" +
		l.T("repos.count", "n", len(list))
	if query != "" {
		text += l.T("repos.search_for", "q", render.Escape(query))
	}
	if pages > 1 {
		text += l.T("repos.page", "n", page+1, "total", pages)
	}

	return ui.View{Text: text, Rows: rows}, nil
}
