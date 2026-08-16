package screens

import (
	"context"
	"time"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type healthScreen struct {
	store HealthStore
	loc   *i18n.Bundle
}

// HealthStore is the narrow read the health screen needs.
type HealthStore interface {
	HealthForIntegration(ctx context.Context, integrationID int64) (storage.IntegrationHealth, error)
}

func NewHealth(store HealthStore, loc *i18n.Bundle) ui.Screen {
	return healthScreen{store: store, loc: loc}
}

func (h healthScreen) Name() string { return "health" }

func (h healthScreen) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	l := h.loc.Localizer(s.Lang)

	integrationID := mustAtoi64(s.Params["integration"])

	health, err := h.store.HealthForIntegration(ctx, integrationID)
	if err != nil {
		return ui.View{}, err
	}

	text := render.Emoji(render.EmojiStats, "🩺") + " <b>" + l.T("health.title") + "</b>\n\n" +
		"📂 " + render.Escape(health.RepoFullName) + " → 💬 " + render.Escape(health.ChatTitle) + "\n"

	if health.BrokenReason != nil {
		text += l.T("health.broken", "reason", render.Escape(*health.BrokenReason)) + "\n"
	}
	if health.MutedUntil != nil && health.MutedUntil.After(time.Now()) {
		text += l.T("health.muted_until",
			"time", health.MutedUntil.Local().Format(l.DateTimeLayout())) + "\n"
	}
	if health.LastEventAt != nil {
		text += l.T("health.last_event",
			"time", health.LastEventAt.Local().Format(l.DateTimeLayout())) + "\n"
	} else {
		text += l.T("health.no_events") + "\n"
	}

	text += "\n" + l.T("health.sent24h", "n", health.Sent24h)
	if health.Failed24h > 0 {
		text += "\n" + l.T("health.failed24h", "n", health.Failed24h)
	}

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{{Label: l.T("btn.events"), Screen: "events", Params: s.Params}},
			{{Label: l.T("btn.home"), Screen: "home"}},
		},
	}, nil
}
