package screens

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type settingsScreen struct {
	chatPerMinute int
}

func NewSettings(chatPerMinute int) ui.Screen {
	return settingsScreen{chatPerMinute: chatPerMinute}
}

// Settings shows the few process-wide facts worth knowing. Star anti-spam is
// not configurable on purpose: one notification per user per repository, ever.
func (s settingsScreen) Name() string { return "settings" }

func (s settingsScreen) Render(_ context.Context, _ ui.Session) (ui.View, error) {
	return ui.View{
		Text: render.Emoji(render.EmojiSettings, "⚙") + " <b>Настройки</b>\n\n" +
			fmt.Sprintf("⭐ Звёзды: одно уведомление на пользователя в репозитории — навсегда\n") +
			fmt.Sprintf("🚦 Лимит отправки: <b>%d</b> сообщений в минуту на чат, остальное сворачивается в дайджест", s.chatPerMinute),
		Rows: [][]ui.Button{
			{{Label: "🏠 В начало", Screen: "home"}},
		},
	}, nil
}
