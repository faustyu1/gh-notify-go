package tg

import (
	"context"
	"fmt"

	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// scope says how a screen or action names the chat it touches. Screens absent
// from the table below are private to the calling user — their own accounts,
// repositories, language — and need no chat authorization.
type scope int

const (
	// scopeChat: params["chat"] is a telegram chat id.
	scopeChat scope = iota + 1
	// scopeIntegration: params["integration"], chat resolved in the database.
	scopeIntegration
	// scopeFilter: params["filter"], chat resolved in the database.
	scopeFilter
)

// chatScopes covers every screen that shows a chat's setup and every action
// that changes it. "connect" is deliberately absent: it carries an internal
// chat id, not a telegram one, and Integrator.Connect authorizes it itself.
var chatScopes = map[string]scope{
	"chat_detail":        scopeChat,
	"a_mute":             scopeChat,
	"a_topic":            scopeChat,
	"integration_detail": scopeIntegration,
	"events":             scopeIntegration,
	"filters":            scopeIntegration,
	"health":             scopeIntegration,
	"a_ev_toggle":        scopeIntegration,
	"a_ev_preset":        scopeIntegration,
	"a_filter_add":       scopeIntegration,
	"a_int_del":          scopeIntegration,
	"a_filter_del":       scopeFilter,
}

// ChatLookup maps a callback parameter back to the chat it belongs to.
type ChatLookup interface {
	TelegramChatForIntegration(ctx context.Context, integrationID int64) (int64, error)
	TelegramChatForFilter(ctx context.Context, filterID int64) (int64, error)
}

// Guard answers "may this user see or change this chat's setup". Every path
// into a chat-scoped screen goes through it: a button tap, the ◁ back button,
// and the /start chat_<id> deep link, which a user can type by hand for any
// chat id at all.
type Guard struct {
	admin service.AdminChecker
	store ChatLookup
}

func NewGuard(admin service.AdminChecker, store ChatLookup) *Guard {
	return &Guard{admin: admin, store: store}
}

// Authorize returns nil when the screen or action is allowed, and
// service.ErrNotAdmin when it is not. Rights are checked at the moment of the
// tap, so a demoted admin loses access even with the old screen in front of
// them.
func (g *Guard) Authorize(
	ctx context.Context, telegramUserID int64, screen string, params ui.Params,
) error {
	sc, scoped := chatScopes[screen]
	if !scoped {
		return nil
	}

	var (
		telegramChatID int64
		err            error
	)
	switch sc {
	case scopeChat:
		telegramChatID = paramInt(params["chat"])
	case scopeIntegration:
		telegramChatID, err = g.store.TelegramChatForIntegration(ctx,
			paramInt(params["integration"]))
	case scopeFilter:
		telegramChatID, err = g.store.TelegramChatForFilter(ctx,
			paramInt(params["filter"]))
	}
	if err != nil {
		return fmt.Errorf("authorize %s: %w", screen, err)
	}
	if telegramChatID == 0 {
		return service.ErrNotAdmin
	}

	admin, err := g.admin.IsAdmin(ctx, telegramChatID, telegramUserID)
	if err != nil {
		return fmt.Errorf("authorize %s: %w", screen, err)
	}
	if !admin {
		return service.ErrNotAdmin
	}
	return nil
}

// Screen adapts the guard to the engine's hook, which authorizes every screen
// render including the ones the back button reaches.
func (g *Guard) Screen() ui.Guard {
	return func(ctx context.Context, telegramID int64, screen string, params ui.Params) error {
		return g.Authorize(ctx, telegramID, screen, params)
	}
}
