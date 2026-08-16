package screens

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// eventPresets sits above the individual toggles because tapping eleven
// buttons is not an interface.
var eventPresets = []struct {
	key    string
	preset string
}{
	{"events.preset_all", "all"},
	{"events.preset_important", "important"},
	{"events.preset_none", "none"},
}

type eventsScreen struct {
	store Store
	loc   *i18n.Bundle
}

func NewEvents(store Store, loc *i18n.Bundle) ui.Screen {
	return eventsScreen{store: store, loc: loc}
}

func (e eventsScreen) Name() string { return "events" }

func (e eventsScreen) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	l := e.loc.Localizer(s.Lang)
	integration := s.Params["integration"]

	settings, err := e.store.EventSettings(ctx, mustAtoi64(integration))
	if err != nil {
		return ui.View{}, err
	}

	kinds := events.Kinds()

	var presetRow []ui.Button
	for _, p := range eventPresets {
		presetRow = append(presetRow, ui.Button{
			Label:  l.T(p.key),
			Screen: "a_ev_preset",
			Params: ui.Params{"integration": integration, "preset": p.preset},
		})
	}

	rows := [][]ui.Button{presetRow}
	for _, kind := range kinds {
		// A kind with no explicit row is on by default.
		enabled, explicit := settings[string(kind)]
		on := !explicit || enabled

		mark := "✅"
		if !on {
			mark = "❌"
		}
		rows = append(rows, []ui.Button{{
			Label:  mark + " " + string(kind),
			Screen: "a_ev_toggle",
			Params: ui.Params{
				"integration": integration,
				"kind":        string(kind),
				"to":          boolStr(!on),
			},
		}})
	}

	return ui.View{
		Text: render.Emoji(render.EmojiBell, "🔔") + " <b>" + l.T("events.title") + "</b>\n\n" +
			render.Escape(s.Params["name"]) + "\n\n" + l.T("events.hint"),
		Rows: rows,
	}, nil
}

func mustAtoi64(s string) int64 {
	var v int64
	_, _ = fmt.Sscanf(s, "%d", &v)
	return v
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
