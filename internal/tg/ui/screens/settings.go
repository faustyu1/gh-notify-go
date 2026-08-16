package screens

import (
	"context"
	"time"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type settingsScreen struct {
	starDebounce  time.Duration
	starCooldown  time.Duration
	chatPerMinute int
}

func NewSettings(starDebounce, starCooldown time.Duration, chatPerMinute int) ui.Screen {
	return settingsScreen{
		starDebounce:  starDebounce,
		starCooldown:  starCooldown,
		chatPerMinute: chatPerMinute,
	}
}

func (s settingsScreen) Name() string { return "settings" }

// The anti-spam knobs are process-wide config, not per-chat choices, so this
// screen shows them rather than offering edits.
func (s settingsScreen) Render(_ context.Context, _ ui.Session) (ui.View, error) {
	return ui.View{
		Text: render.Emoji(render.EmojiSettings, "⚙") + " <b>Настройки</b>\n\n" +
			"Анти-спам звёзд:\n" +
			"· окно накопления — <b>" + s.starDebounce.String() + "</b>\n" +
			"· кулдаун на автора — <b>" + (s.starCooldown / time.Hour).String() + "ч</b>\n" +
			"· несколько авторов складываются в одно сообщение\n\n" +
			"Лимит отправки: <b>" + render.Escape(fmtInt(s.chatPerMinute)) + "</b> сообщений в минуту на чат",
		Rows: [][]ui.Button{
			{{Label: "🏠 В начало", Screen: "home"}},
		},
	}, nil
}

func fmtInt(v int) string {
	if v == 0 {
		return "0"
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}
