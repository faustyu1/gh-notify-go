package tg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/tg"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// fakeAdmin records what it was asked, so a test can prove the guard checked
// the chat the data actually belongs to.
type fakeAdmin struct {
	admins map[int64]struct{} // telegram chat ids where the caller is admin
	asked  []int64
}

func (f *fakeAdmin) IsAdmin(_ context.Context, telegramChatID, _ int64) (bool, error) {
	f.asked = append(f.asked, telegramChatID)
	_, ok := f.admins[telegramChatID]
	return ok, nil
}

// fakeLookup stands in for the database: integration 7 and filter 3 live in
// chat -100.
type fakeLookup struct{}

func (fakeLookup) TelegramChatForIntegration(_ context.Context, id int64) (int64, error) {
	if id == 7 {
		return -100, nil
	}
	return 0, errors.New("integration not found")
}

func (fakeLookup) TelegramChatForFilter(_ context.Context, id int64) (int64, error) {
	if id == 3 {
		return -100, nil
	}
	return 0, errors.New("filter not found")
}

func newGuard(adminOf ...int64) (*tg.Guard, *fakeAdmin) {
	admin := &fakeAdmin{admins: map[int64]struct{}{}}
	for _, id := range adminOf {
		admin.admins[id] = struct{}{}
	}
	return tg.NewGuard(admin, fakeLookup{}), admin
}

func TestGuardAllowsUserPrivateScreens(t *testing.T) {
	guard, admin := newGuard()

	for _, screen := range []string{"home", "accounts", "repos", "settings", "status"} {
		require.NoError(t, guard.Authorize(context.Background(), 555, screen, nil), screen)
	}
	require.Empty(t, admin.asked, "a private screen must not cost a Telegram call")
}

// The deep link /start chat_<id> puts an arbitrary chat id in the params, so
// the screen behind it has to be authorized like any action.
func TestGuardRefusesChatScreenForNonAdmin(t *testing.T) {
	guard, _ := newGuard()

	err := guard.Authorize(context.Background(), 555, "chat_detail", ui.Params{"chat": "-100"})
	require.ErrorIs(t, err, service.ErrNotAdmin)
}

func TestGuardAllowsChatScreenForAdmin(t *testing.T) {
	guard, admin := newGuard(-100)

	require.NoError(t, guard.Authorize(context.Background(), 555,
		"chat_detail", ui.Params{"chat": "-100"}))
	require.Equal(t, []int64{-100}, admin.asked)
}

func TestGuardRefusesEveryChatChangingAction(t *testing.T) {
	guard, _ := newGuard(-999) // admin somewhere else, nothing to do with -100

	cases := []struct {
		screen string
		params ui.Params
	}{
		{"a_mute", ui.Params{"chat": "-100", "hours": "24"}},
		{"a_topic", ui.Params{"chat": "-100"}},
		{"a_ev_toggle", ui.Params{"integration": "7", "kind": "push", "to": "0"}},
		{"a_ev_preset", ui.Params{"integration": "7", "preset": "none"}},
		{"a_filter_add", ui.Params{"integration": "7", "kind": "author"}},
		{"a_filter_del", ui.Params{"filter": "3"}},
		{"a_int_del", ui.Params{"integration": "7"}},
		{"events", ui.Params{"integration": "7"}},
		{"filters", ui.Params{"integration": "7"}},
		{"health", ui.Params{"integration": "7"}},
		{"integration_detail", ui.Params{"integration": "7"}},
	}
	for _, c := range cases {
		err := guard.Authorize(context.Background(), 555, c.screen, c.params)
		require.ErrorIs(t, err, service.ErrNotAdmin, c.screen)
	}
}

// A forged chat param must not decide which chat's admin list is consulted:
// the integration's own chat does.
func TestGuardIgnoresChatParamOnIntegrationScope(t *testing.T) {
	guard, admin := newGuard(-777)

	err := guard.Authorize(context.Background(), 555, "a_int_del",
		ui.Params{"integration": "7", "chat": "-777"})
	require.ErrorIs(t, err, service.ErrNotAdmin)
	require.Equal(t, []int64{-100}, admin.asked,
		"the guard must ask about the integration's chat, not the one in params")
}

func TestGuardRefusesUnknownIntegration(t *testing.T) {
	guard, admin := newGuard(-100)

	err := guard.Authorize(context.Background(), 555, "events", ui.Params{"integration": "42"})
	require.Error(t, err)
	require.Empty(t, admin.asked)
}

func TestGuardRefusesMissingChatParam(t *testing.T) {
	guard, _ := newGuard(-100)

	err := guard.Authorize(context.Background(), 555, "chat_detail", nil)
	require.ErrorIs(t, err, service.ErrNotAdmin)
}
