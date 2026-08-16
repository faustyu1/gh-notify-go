package ui_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

// stubScreen renders its own name plus whatever params it received, which is
// enough to assert routing without pulling in real screens.
type stubScreen struct {
	name string
	rows [][]ui.Button
}

func (s stubScreen) Name() string { return s.name }

func (s stubScreen) Render(_ context.Context, sess ui.Session) (ui.View, error) {
	return ui.View{Text: s.name + ":" + sess.Params["id"], Rows: s.rows}, nil
}

func newEngine(t *testing.T) (*ui.Engine, *pgxpool.Pool, int64) {
	t.Helper()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, testhelper.StartPostgres(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var userID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (telegram_id) VALUES (555) RETURNING id`).Scan(&userID))

	engine := ui.NewEngine(ui.NewPostgresNav(pool))
	engine.Register(
		stubScreen{name: "home", rows: [][]ui.Button{{
			{Label: "Репозитории", Screen: "repos"},
		}}},
		stubScreen{name: "repos"},
		stubScreen{name: "repo_detail"},
	)
	return engine, pool, userID
}

func TestOpenRendersScreenAndPushesStack(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	view, err := engine.Open(ctx, userID, 555, "home", nil)
	require.NoError(t, err)
	require.Equal(t, "home:", view.Text)
}

func TestHomeHasNoBackButton(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	view, err := engine.Open(ctx, userID, 555, "home", nil)
	require.NoError(t, err)

	for _, row := range view.Rows {
		for _, b := range row {
			require.NotEqual(t, ui.BackButtonLabel, b.Label)
		}
	}
}

func TestDeeperScreensGetBackButton(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	_, err := engine.Open(ctx, userID, 555, "home", nil)
	require.NoError(t, err)

	view, err := engine.Open(ctx, userID, 555, "repos", ui.Params{"id": "7"})
	require.NoError(t, err)
	require.Equal(t, "repos:7", view.Text)

	last := view.Rows[len(view.Rows)-1]
	require.Equal(t, ui.BackButtonLabel, last[0].Label)
}

func TestBackReturnsToPreviousScreenWithItsParams(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	_, err := engine.Open(ctx, userID, 555, "home", nil)
	require.NoError(t, err)
	_, err = engine.Open(ctx, userID, 555, "repos", ui.Params{"id": "7"})
	require.NoError(t, err)
	_, err = engine.Open(ctx, userID, 555, "repo_detail", ui.Params{"id": "99"})
	require.NoError(t, err)

	view, err := engine.Back(ctx, userID, 555)
	require.NoError(t, err)
	require.Equal(t, "repos:7", view.Text)

	view, err = engine.Back(ctx, userID, 555)
	require.NoError(t, err)
	require.Equal(t, "home:", view.Text)
}

func TestBackAtHomeStaysAtHome(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	_, err := engine.Open(ctx, userID, 555, "home", nil)
	require.NoError(t, err)

	view, err := engine.Back(ctx, userID, 555)
	require.NoError(t, err)
	require.Equal(t, "home:", view.Text)
}

func TestActionKeyIsShortEnoughForCallbackData(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	key, err := engine.ActionKey(ctx, userID, "repo_detail",
		ui.Params{"id": "a-very-long-repository-identifier-that-would-never-fit"})
	require.NoError(t, err)
	// Telegram caps callback_data at 64 bytes.
	require.LessOrEqual(t, len(key), 64)
}

func TestResolveReturnsStoredScreenAndParams(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	key, err := engine.ActionKey(ctx, userID, "repo_detail", ui.Params{"id": "99"})
	require.NoError(t, err)

	screen, params, err := engine.Resolve(ctx, userID, key)
	require.NoError(t, err)
	require.Equal(t, "repo_detail", screen)
	require.Equal(t, "99", params["id"])
}

func TestResolveRejectsAnotherUsersKey(t *testing.T) {
	ctx := context.Background()
	engine, pool, userID := newEngine(t)

	var otherID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (telegram_id) VALUES (999) RETURNING id`).Scan(&otherID))

	key, err := engine.ActionKey(ctx, userID, "repo_detail", ui.Params{"id": "99"})
	require.NoError(t, err)

	_, _, err = engine.Resolve(ctx, otherID, key)
	require.ErrorIs(t, err, ui.ErrActionNotFound)
}

func TestOpenUnknownScreenIsAnError(t *testing.T) {
	ctx := context.Background()
	engine, _, userID := newEngine(t)

	_, err := engine.Open(ctx, userID, 555, "nope", nil)
	require.ErrorIs(t, err, ui.ErrUnknownScreen)
}

func TestAnchorMessageIDRoundTrips(t *testing.T) {
	ctx := context.Background()
	_, pool, userID := newEngine(t)
	nav := ui.NewPostgresNav(pool)

	id, err := nav.AnchorMessageID(ctx, userID)
	require.NoError(t, err)
	require.Zero(t, id)

	require.NoError(t, nav.SetAnchorMessageID(ctx, userID, 4242))
	id, err = nav.AnchorMessageID(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, 4242, id)
}
