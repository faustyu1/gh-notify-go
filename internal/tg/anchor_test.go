package tg_test

import (
	"context"
	"testing"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegoapi"
	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/tg"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// fakeAnchorAPI records sends and edits separately so the test can tell which
// path the anchor took.
type fakeAnchorAPI struct {
	sent    []*telego.SendMessageParams
	edited  []*telego.EditMessageTextParams
	editErr error
}

func (f *fakeAnchorAPI) SendMessage(
	_ context.Context, p *telego.SendMessageParams,
) (*telego.Message, error) {
	f.sent = append(f.sent, p)
	return &telego.Message{MessageID: 100 + len(f.sent)}, nil
}

func (f *fakeAnchorAPI) EditMessageText(
	_ context.Context, p *telego.EditMessageTextParams,
) (*telego.Message, error) {
	f.edited = append(f.edited, p)
	if f.editErr != nil {
		return nil, f.editErr
	}
	return &telego.Message{MessageID: p.MessageID}, nil
}

// memNav is an in-memory NavStore; the anchor logic needs no database.
type memNav struct {
	anchor  int
	actions map[string][2]string
}

func newMemNav() *memNav { return &memNav{actions: map[string][2]string{}} }

func (m *memNav) Push(context.Context, int64, string, ui.Params) error { return nil }
func (m *memNav) Pop(context.Context, int64) (string, ui.Params, error) {
	return "home", nil, nil
}
func (m *memNav) Depth(context.Context, int64) (int, error) { return 1, nil }
func (m *memNav) PutAction(_ context.Context, _ int64, key, screen string, _ ui.Params) error {
	m.actions[key] = [2]string{screen, ""}
	return nil
}
func (m *memNav) GetAction(_ context.Context, _ int64, key string) (string, ui.Params, error) {
	v, ok := m.actions[key]
	if !ok {
		return "", nil, ui.ErrActionNotFound
	}
	return v[0], nil, nil
}
func (m *memNav) AnchorMessageID(context.Context, int64) (int, error) { return m.anchor, nil }
func (m *memNav) SetAnchorMessageID(_ context.Context, _ int64, id int) error {
	m.anchor = id
	return nil
}

func TestShowSendsWhenNoAnchorExists(t *testing.T) {
	api := &fakeAnchorAPI{}
	nav := newMemNav()
	anchor := tg.NewAnchor(api, ui.NewEngine(nav, i18n.MustNewBundle()), nav)

	err := anchor.Show(context.Background(), 1, 555, ui.View{
		Text: "hello", Rows: [][]ui.Button{{{Label: "Go", Screen: "home"}}},
	})
	require.NoError(t, err)
	require.Len(t, api.sent, 1)
	require.Empty(t, api.edited)
	require.Equal(t, 101, nav.anchor, "the new message becomes the anchor")
}

func TestShowEditsExistingAnchor(t *testing.T) {
	api := &fakeAnchorAPI{}
	nav := newMemNav()
	nav.anchor = 42
	anchor := tg.NewAnchor(api, ui.NewEngine(nav, i18n.MustNewBundle()), nav)

	require.NoError(t, anchor.Show(context.Background(), 1, 555, ui.View{Text: "hi"}))
	require.Empty(t, api.sent)
	require.Len(t, api.edited, 1)
	require.Equal(t, 42, api.edited[0].MessageID)
}

func TestShowFallsBackToSendWhenAnchorIsGone(t *testing.T) {
	// The user deleted the anchor message; editing it fails permanently, so
	// a fresh one must take its place instead of the screen going dead.
	api := &fakeAnchorAPI{editErr: &telegoapi.Error{
		ErrorCode:   400,
		Description: "Bad Request: message to edit not found",
	}}
	nav := newMemNav()
	nav.anchor = 42
	anchor := tg.NewAnchor(api, ui.NewEngine(nav, i18n.MustNewBundle()), nav)

	require.NoError(t, anchor.Show(context.Background(), 1, 555, ui.View{Text: "hi"}))
	require.Len(t, api.edited, 1)
	require.Len(t, api.sent, 1)
	require.Equal(t, 101, nav.anchor)
}

func TestShowIgnoresUnchangedContent(t *testing.T) {
	// Telegram rejects an edit that changes nothing; tapping the current
	// screen's own button must not surface as an error.
	api := &fakeAnchorAPI{editErr: &telegoapi.Error{
		ErrorCode:   400,
		Description: "Bad Request: message is not modified",
	}}
	nav := newMemNav()
	nav.anchor = 42
	anchor := tg.NewAnchor(api, ui.NewEngine(nav, i18n.MustNewBundle()), nav)

	require.NoError(t, anchor.Show(context.Background(), 1, 555, ui.View{Text: "hi"}))
	require.Empty(t, api.sent)
}

func TestKeyboardUsesOpaqueCallbackKeys(t *testing.T) {
	nav := newMemNav()
	engine := ui.NewEngine(nav, i18n.MustNewBundle())

	markup, err := tg.Keyboard(context.Background(), engine, 1, ui.View{
		Rows: [][]ui.Button{
			{{Label: "Go", Screen: "repos", Params: ui.Params{"installation": "1"}}},
			{{Label: "GitHub", URL: "https://github.com"}},
		},
	})
	require.NoError(t, err)
	require.Len(t, markup.InlineKeyboard, 2)

	navButton := markup.InlineKeyboard[0][0]
	require.NotEmpty(t, navButton.CallbackData)
	require.LessOrEqual(t, len(navButton.CallbackData), 64)
	require.NotContains(t, navButton.CallbackData, "installation")

	linkButton := markup.InlineKeyboard[1][0]
	require.Equal(t, "https://github.com", linkButton.URL)
	require.Empty(t, linkButton.CallbackData)
}
