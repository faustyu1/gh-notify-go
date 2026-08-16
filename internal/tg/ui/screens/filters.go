package screens

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type filtersScreen struct {
	store Store
	loc   *i18n.Bundle
}

func NewFilters(store Store, loc *i18n.Bundle) ui.Screen {
	return filtersScreen{store: store, loc: loc}
}

func (f filtersScreen) Name() string { return "filters" }

// filterKinds maps the stored kind to its i18n key.
var filterKinds = []struct {
	kind string
	key  string
}{
	{"author", "filters.kind.author"},
	{"branch", "filters.kind.branch"},
	{"label", "filters.kind.label"},
	{"action", "filters.kind.action"},
}

func (f filtersScreen) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	l := f.loc.Localizer(s.Lang)
	integration := s.Params["integration"]

	list, err := f.store.FiltersForIntegration(ctx, mustAtoi64(integration))
	if err != nil {
		return ui.View{}, err
	}

	text := render.Emoji(render.EmojiCross, "🚫") + " <b>" + l.T("filters.title") + "</b>\n\n" +
		render.Escape(s.Params["name"]) + "\n\n"
	var rows [][]ui.Button
	if len(list) == 0 {
		text += l.T("filters.empty")
	} else {
		text += l.T("filters.hint")
		for _, flt := range list {
			rows = append(rows, []ui.Button{{
				Label: l.T("filters.entry",
					"kind", kindLabel(l, flt.Kind), "value", flt.Value),
				Screen: "a_filter_del",
				Params: ui.Params{
					"filter":      fmt.Sprint(flt.ID),
					"integration": integration,
					"name":        s.Params["name"],
				},
			}})
		}
	}

	var addRow []ui.Button
	for _, k := range filterKinds {
		addRow = append(addRow, ui.Button{
			Label:  l.T("filters.add", "label", l.T(k.key)),
			Screen: "a_filter_add",
			Params: ui.Params{"integration": integration, "kind": k.kind, "name": s.Params["name"]},
		})
	}
	rows = append(rows, addRow)

	return ui.View{Text: text, Rows: rows}, nil
}

func kindLabel(l *i18n.Localizer, kind string) string {
	for _, k := range filterKinds {
		if k.kind == kind {
			return l.T(k.key)
		}
	}
	return kind
}
