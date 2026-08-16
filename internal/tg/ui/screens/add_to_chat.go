package screens

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// addToChat explains how the bot gets into a group. The heavy lifting is a
// Telegram deep link: startgroup opens the client's own group picker, and the
// my_chat_member handler records the chat and greets it once the bot lands
// there.
type addToChat struct {
	botUser string
	loc     *i18n.Bundle
}

func NewAddToChat(botUser string, loc *i18n.Bundle) ui.Screen {
	return addToChat{botUser: botUser, loc: loc}
}

func (a addToChat) Name() string { return "add_to_chat" }

func (a addToChat) Render(_ context.Context, s ui.Session) (ui.View, error) {
	l := a.loc.Localizer(s.Lang)

	text := render.Emoji(render.EmojiPeople, "👥") + " <b>" + l.T("add_to_chat.title") +
		"</b>\n\n" + l.T("add_to_chat.body")

	return ui.View{
		Text: text,
		Rows: [][]ui.Button{{
			{
				Label: l.T("btn.add_to_group"),
				URL:   fmt.Sprintf("https://t.me/%s?startgroup=add", a.botUser),
			},
		}},
	}, nil
}
