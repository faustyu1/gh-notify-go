package screens

import (
	"context"
	"fmt"
	"time"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type healthScreen struct{ store HealthStore }

// HealthStore is the narrow read the health screen needs.
type HealthStore interface {
	HealthForIntegration(ctx context.Context, integrationID int64) (storage.IntegrationHealth, error)
}

func NewHealth(store HealthStore) ui.Screen { return healthScreen{store: store} }

func (h healthScreen) Name() string { return "health" }

func (h healthScreen) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	integrationID := mustAtoi64(s.Params["integration"])

	health, err := h.store.HealthForIntegration(ctx, integrationID)
	if err != nil {
		return ui.View{}, err
	}

	text := render.Emoji(render.EmojiStats, "🩺") + " <b>Здоровье</b>\n\n" +
		"📂 " + render.Escape(health.RepoFullName) + " → 💬 " + render.Escape(health.ChatTitle) + "\n"

	if health.BrokenReason != nil {
		text += "⚠️ <b>Сломана:</b> " + render.Escape(*health.BrokenReason) + "\n"
	}
	if health.MutedUntil != nil && health.MutedUntil.After(time.Now()) {
		text += "🔇 Чат замьютен до " + health.MutedUntil.Local().Format("02.01 15:04") + "\n"
	}
	if health.LastEventAt != nil {
		text += "🕐 Последнее событие: " + health.LastEventAt.Local().Format("02.01 15:04") + "\n"
	} else {
		text += "🕐 Событий ещё не было\n"
	}

	text += fmt.Sprintf("\n✅ Доставлено за 24ч: <b>%d</b>", health.Sent24h)
	if health.Failed24h > 0 {
		text += fmt.Sprintf("\n❌ Не доставлено: <b>%d</b>", health.Failed24h)
	}

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{{Label: "🔔 События", Screen: "events", Params: s.Params}},
			{{Label: "🏠 В начало", Screen: "home"}},
		},
	}, nil
}
