package screens

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type filtersScreen struct{ store Store }

func NewFilters(store Store) ui.Screen { return filtersScreen{store: store} }

func (f filtersScreen) Name() string { return "filters" }

var filterKindLabels = []struct {
	kind  string
	label string
	hint  string
}{
	{"author", "👤 Автор", "логин, например dependabot[bot] или octo*"},
	{"branch", "🌿 Ветка", "имя ветки, можно с *, например renovate/*"},
	{"label", "🏷 Лейбл", "имя лейбла, например wip"},
}

func (f filtersScreen) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	integration := s.Params["integration"]

	list, err := f.store.FiltersForIntegration(ctx, mustAtoi64(integration))
	if err != nil {
		return ui.View{}, err
	}

	text := render.Emoji(render.EmojiCross, "🚫") + " <b>Фильтры</b>\n\n" +
		render.Escape(s.Params["name"]) + "\n\n"
	var rows [][]ui.Button
	if len(list) == 0 {
		text += "Пока ничего не игнорируется."
	} else {
		text += "Совпавшие события не присылаются:"
		for _, flt := range list {
			rows = append(rows, []ui.Button{{
				Label: fmt.Sprintf("✖ %s: %s", kindLabel(flt.Kind), flt.Value),
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
	for _, k := range filterKindLabels {
		addRow = append(addRow, ui.Button{
			Label:  "+ " + k.label,
			Screen: "a_filter_add",
			Params: ui.Params{"integration": integration, "kind": k.kind, "name": s.Params["name"]},
		})
	}
	rows = append(rows, addRow)

	return ui.View{Text: text, Rows: rows}, nil
}

func kindLabel(kind string) string {
	for _, k := range filterKindLabels {
		if k.kind == kind {
			return k.label
		}
	}
	return kind
}
