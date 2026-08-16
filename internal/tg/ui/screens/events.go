package screens

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// eventPresets sits above the individual toggles because tapping eleven
// buttons is not an interface.
var eventPresets = []struct {
	label  string
	preset string
}{
	{"✅ Всё", "all"},
	{"⭐ Только важное", "important"},
	{"❌ Ничего", "none"},
}

// importantKinds is the «Только важное» preset: what a chat usually wants.
var importantKinds = map[events.Kind]bool{
	"pull_request": true, "issues": true, "issue_comment": true,
	"pull_request_review": true, "release": true, "workflow_run": true,
}

type eventsScreen struct{ store Store }

func NewEvents(store Store) ui.Screen { return eventsScreen{store: store} }

func (e eventsScreen) Name() string { return "events" }

func (e eventsScreen) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	integration := s.Params["integration"]

	settings, err := e.store.EventSettings(ctx, mustAtoi64(integration))
	if err != nil {
		return ui.View{}, err
	}

	kinds := events.Kinds()

	var presetRow []ui.Button
	for _, p := range eventPresets {
		presetRow = append(presetRow, ui.Button{
			Label:  p.label,
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
			Label: mark + " " + string(kind),
			Screen: "a_ev_toggle",
			Params: ui.Params{
				"integration": integration,
				"kind":        string(kind),
				"to":          boolStr(!on),
			},
		}})
	}

	return ui.View{
		Text: render.Emoji(render.EmojiBell, "🔔") + " <b>События</b>\n\n" +
			render.Escape(s.Params["name"]) +
			fmt.Sprintf("\n\nПресеты сверху, отдельные типы ниже."),
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
