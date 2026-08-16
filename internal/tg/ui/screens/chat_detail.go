package screens

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type chatDetail struct{ store Store }

func NewChatDetail(store Store) ui.Screen { return chatDetail{store: store} }

func (d chatDetail) Name() string { return "chat_detail" }

func (d chatDetail) Render(ctx context.Context, s ui.Session) (ui.View, error) {
	telegramChatID, err := strconv.ParseInt(s.Params["chat"], 10, 64)
	if err != nil {
		return ui.View{}, fmt.Errorf("chat_detail: bad chat param: %w", err)
	}

	chat, err := d.store.ChatByTelegramID(ctx, telegramChatID)
	if err != nil {
		return ui.View{}, err
	}
	integrations, err := d.store.IntegrationsInChat(ctx, chat.ID)
	if err != nil {
		return ui.View{}, err
	}

	var b fmtBuilder
	b.line(render.Emoji(render.EmojiPeople, "👥") + " <b>" + render.Escape(chat.Title) + "</b>")

	if chat.MutedUntil != nil && chat.MutedUntil.After(time.Now()) {
		b.line(render.Emoji(render.EmojiHidden, "🔇") + " Замьютен до " +
			chat.MutedUntil.Local().Format("02.01 15:04"))
	} else {
		b.line("Уведомления активны")
	}
	if chat.TopicID != nil {
		b.line("Топик: <code>" + strconv.FormatInt(*chat.TopicID, 10) + "</code>")
	}
	b.line("")

	rows := make([][]ui.Button, 0, len(integrations)+4)
	for _, it := range integrations {
		icon := "📂"
		if it.BrokenReason != nil {
			icon = "⚠️"
		}
		rows = append(rows, []ui.Button{{
			Label:  icon + " " + it.RepoFullName,
			Screen: "integration_detail",
			Params: ui.Params{
				"integration": strconv.FormatInt(it.ID, 10),
				"chat":        s.Params["chat"],
				"name":        it.RepoFullName,
			},
		}})
	}

	// Mute presets. Action buttons carry the chat id; the handler applies the
	// window and reopens this screen.
	mute := []ui.Button{
		{Label: "🔇 1ч", Screen: "a_mute", Params: ui.Params{"chat": s.Params["chat"], "hours": "1"}},
		{Label: "🔇 8ч", Screen: "a_mute", Params: ui.Params{"chat": s.Params["chat"], "hours": "8"}},
		{Label: "🔇 24ч", Screen: "a_mute", Params: ui.Params{"chat": s.Params["chat"], "hours": "24"}},
		{Label: "🔔 Снять", Screen: "a_mute", Params: ui.Params{"chat": s.Params["chat"], "hours": "0"}},
	}
	rows = append(rows, mute)
	rows = append(rows, []ui.Button{{
		Label:  "🏷 Указать топик",
		Screen: "a_topic",
		Params: ui.Params{"chat": s.Params["chat"]},
	}})
	if len(integrations) == 0 {
		rows = append(rows, []ui.Button{{Label: "🏢 Репозитории", Screen: "accounts"}})
	}

	return ui.View{Text: b.String(), Rows: rows}, nil
}

// fmtBuilder is a tiny helper so multi-line texts stay readable.
type fmtBuilder struct{ lines []string }

func (f *fmtBuilder) line(s string) { f.lines = append(f.lines, s) }

func (f *fmtBuilder) String() string {
	out := ""
	for i, l := range f.lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
