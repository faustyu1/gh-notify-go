package screens

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type statusScreen struct{ store Store }

func NewStatus(store Store) ui.Screen { return statusScreen{store: store} }

func (s statusScreen) Name() string { return "status" }

func (s statusScreen) Render(ctx context.Context, sess ui.Session) (ui.View, error) {
	accounts, repos, chats, err := s.store.CountsForUser(ctx, sess.UserID)
	if err != nil {
		return ui.View{}, err
	}
	sent, failed, err := s.store.StatusStats(ctx, sess.UserID)
	if err != nil {
		return ui.View{}, err
	}

	text := fmt.Sprintf(
		"%s <b>Статус</b>\n\n"+
			"%s Аккаунтов: <b>%d</b> · репозиториев: <b>%d</b> · чатов: <b>%d</b>\n"+
			"%s За 24 часа доставлено: <b>%d</b>",
		render.Emoji(render.EmojiStats, "📊"),
		render.Emoji(render.EmojiProfile, "👤"), accounts, repos, chats,
		render.Emoji(render.EmojiCheck, "✅"), sent,
	)
	if failed > 0 {
		text += fmt.Sprintf("\n%s Не доставлено: <b>%d</b> — смотри раздел «Чаты»",
			render.Emoji(render.EmojiCross, "❌"), failed)
	}

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{
			{{Label: "💬 Чаты", Screen: "chats"}, {Label: "🏢 Репозитории", Screen: "accounts"}},
		},
	}, nil
}
