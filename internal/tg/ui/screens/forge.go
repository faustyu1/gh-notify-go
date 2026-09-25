package screens

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/faustyu/gh-notify-go/internal/domain"
	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/forge"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// The screens below serve every webhook-connected provider: which one a
// connection belongs to comes from the connection itself, so buttons need
// carry no more than its id. Their old gl_ names stay registered as aliases,
// because menus sent before the rename still carry them.

// forgeName labels a connection: a webhook gives no account name, so it is
// named after the namespace of the first repository it delivered for.
func forgeName(l *i18n.Localizer, it domain.Installation) string {
	provider := forge.Name(it.Provider)
	if it.AccountLogin == "" {
		return provider
	}
	return l.T("forge.projects_title", "provider", provider, "name", it.AccountLogin)
}

// forgeIcon is the fallback glyph shown for a provider's connections.
func forgeIcon(provider string) string {
	switch provider {
	case domain.ProviderGitLab:
		return "🦊"
	case "gitea", "forgejo":
		return "🍵"
	}
	return "🧩"
}

// Alias registers a screen under one more name.
func Alias(name string, s ui.Screen) ui.Screen { return alias{Screen: s, name: name} }

type alias struct {
	ui.Screen
	name string
}

func (a alias) Name() string { return a.name }

func installationParam(s ui.Session, screen string) (int64, error) {
	id, err := strconv.ParseInt(s.Params["installation"], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s screen: bad installation param: %w", screen, err)
	}
	return id, nil
}

type connect struct {
	loc *i18n.Bundle
}

// NewConnect offers the providers connected by a webhook.
func NewConnect(loc *i18n.Bundle) ui.Screen { return connect{loc: loc} }

func (c connect) Name() string { return "connect" }

func (c connect) Render(_ context.Context, s ui.Session) (ui.View, error) {
	l := c.loc.Localizer(s.Lang)
	var rows [][]ui.Button
	for _, h := range forge.Hooks() {
		rows = append(rows, []ui.Button{{
			Label: h.Name(), Icon: render.EmojiCode, Screen: "a_forge_new",
			Params: ui.Params{"provider": h.ID()},
		}})
	}
	return ui.View{
		Text: render.Emoji(render.EmojiLink, "🔗") + " <b>" + l.T("connect.title") +
			"</b>\n\n" + l.T("connect.body"),
		Rows: rows,
	}, nil
}

type hookSetup struct {
	publicURL string
	store     Store
	forge     Forge
	loc       *i18n.Bundle
}

// NewHookSetup shows the owner what to paste into the host's webhook form.
func NewHookSetup(publicURL string, store Store, forge Forge, loc *i18n.Bundle) ui.Screen {
	return hookSetup{publicURL: strings.TrimRight(publicURL, "/"), store: store, forge: forge, loc: loc}
}

func (h hookSetup) Name() string { return "hook_setup" }

func (h hookSetup) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	l := h.loc.Localizer(s.Lang)
	installationID, err := installationParam(s, h.Name())
	if err != nil {
		return ui.View{}, err
	}
	installation, err := h.store.InstallationByID(ctx, installationID)
	if err != nil {
		return ui.View{}, err
	}
	hook, ok := forge.HookFor(installation.Provider)
	if !ok {
		return ui.View{}, fmt.Errorf("hook_setup screen: %q has no webhook", installation.Provider)
	}

	// Scoped to the owner: the secret is what lets anyone post in their name.
	token, err := h.forge.WebhookToken(ctx, installationID, s.UserID)
	if err != nil {
		return ui.View{}, err
	}

	params := ui.Params{"installation": s.Params["installation"]}
	return ui.View{
		Text: render.Emoji(render.EmojiCode, forgeIcon(hook.ID())) + " <b>" +
			l.T("forge.hook_title", "name", hook.Name()) + "</b>\n\n" +
			l.T("forge.hook."+hook.ID(),
				"url", render.Escape(h.publicURL+forge.WebhookPath(hook, installationID)),
				"token", render.Escape(token)),
		Rows: [][]ui.Button{
			{{Label: l.T("btn.forge_projects"), Icon: render.EmojiFile,
				Screen: "forge_projects", Params: params}},
			{{Label: l.T("btn.forge_delete"), Icon: render.EmojiTrash,
				Screen: "forge_delete", Params: params}},
		},
	}, nil
}

type forgeProjects struct {
	store    Store
	forge    Forge
	pageSize int
	loc      *i18n.Bundle
}

// NewForgeProjects lists the repositories a webhook connection has delivered
// for; it is the webhook counterpart of the repos screen.
func NewForgeProjects(store Store, forge Forge, pageSize int, loc *i18n.Bundle) ui.Screen {
	return forgeProjects{store: store, forge: forge, pageSize: pageSize, loc: loc}
}

func (f forgeProjects) Name() string { return "forge_projects" }

func (f forgeProjects) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	l := f.loc.Localizer(s.Lang)
	installationID, err := installationParam(s, f.Name())
	if err != nil {
		return ui.View{}, err
	}

	installation, err := f.store.InstallationByID(ctx, installationID)
	if err != nil {
		return ui.View{}, err
	}
	list, err := f.forge.Projects(ctx, installationID, s.UserID)
	if err != nil {
		return ui.View{}, err
	}

	base := ui.Params{"installation": s.Params["installation"]}
	footer := [][]ui.Button{
		{
			{Label: l.T("btn.refresh"), Icon: render.EmojiRefresh, Screen: "a_refresh",
				Params: ui.Params{"screen": "forge_projects",
					"installation": s.Params["installation"], "page": s.Params["page"]}},
			{Label: l.T("btn.forge_webhook"), Icon: render.EmojiSettings,
				Screen: "hook_setup", Params: base},
		},
	}
	title := render.Emoji(render.EmojiCode, forgeIcon(installation.Provider)) + " <b>" +
		render.Escape(forgeName(l, installation)) + "</b>\n\n"

	if len(list) == 0 {
		return ui.View{
			Text: title + l.T("forge.projects_empty", "provider", forge.Name(installation.Provider)),
			Rows: footer,
		}, nil
	}

	page, _ := strconv.Atoi(s.Params["page"])
	pages := (len(list) + f.pageSize - 1) / f.pageSize
	page = max(0, min(page, pages-1))
	start := page * f.pageSize
	end := min(start+f.pageSize, len(list))

	rows := make([][]ui.Button, 0, f.pageSize+3)
	for _, p := range list[start:end] {
		rows = append(rows, []ui.Button{{
			Label:  p.Path,
			Icon:   render.EmojiFolder,
			Screen: "repo_detail",
			Params: ui.Params{
				"installation": s.Params["installation"],
				"repo":         strconv.FormatInt(p.ID, 10),
				"name":         p.Path,
				"provider":     installation.Provider,
				"url":          p.WebURL,
			},
		}})
	}

	if pages > 1 {
		var nav []ui.Button
		if page > 0 {
			nav = append(nav, ui.Button{Label: l.T("btn.prev_page"), Icon: render.EmojiBack,
				Screen: "forge_projects", Params: ui.Params{
					"installation": s.Params["installation"], "page": strconv.Itoa(page - 1)}})
		}
		if page < pages-1 {
			nav = append(nav, ui.Button{Label: l.T("btn.next_page"), Icon: render.EmojiForward,
				Screen: "forge_projects", Params: ui.Params{
					"installation": s.Params["installation"], "page": strconv.Itoa(page + 1)}})
		}
		rows = append(rows, nav)
	}

	text := title + l.T("forge.projects_count", "n", len(list))
	if pages > 1 {
		text += l.T("repos.page", "n", page+1, "total", pages)
	}
	return ui.View{Text: text, Rows: append(rows, footer...)}, nil
}

type forgeDelete struct {
	store Store
	loc   *i18n.Bundle
}

// NewForgeDelete asks before a connection goes: deleting it silences every
// chat its repositories deliver to.
func NewForgeDelete(store Store, loc *i18n.Bundle) ui.Screen {
	return forgeDelete{store: store, loc: loc}
}

func (f forgeDelete) Name() string { return "forge_delete" }

func (f forgeDelete) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	l := f.loc.Localizer(s.Lang)
	installationID, err := installationParam(s, f.Name())
	if err != nil {
		return ui.View{}, err
	}
	installation, err := f.store.InstallationByID(ctx, installationID)
	if err != nil {
		return ui.View{}, err
	}
	return ui.View{
		Text: render.Emoji(render.EmojiWarning, "⚠️") + " " +
			l.T("forge.delete_confirm", "provider", forge.Name(installation.Provider)),
		Rows: [][]ui.Button{{{
			Label: l.T("btn.forge_delete_yes"), Icon: render.EmojiTrash, Screen: "a_forge_del",
			Params: ui.Params{"installation": s.Params["installation"]},
		}}},
	}, nil
}
