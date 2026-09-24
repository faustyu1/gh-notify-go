package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/storage"
)

func TestRefLinkAttributesOnlyNewUsers(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	admin, _, err := store.UpsertUser(ctx, 1, "ru")
	require.NoError(t, err)
	link, err := store.CreateRefLink(ctx, admin, "channel")
	require.NoError(t, err)
	require.Len(t, link.Code, 8)

	// A newcomer through the link is attributed; the admin, who already
	// existed, only adds a tap.
	_, lang, err := store.UpsertUserWithRef(ctx, 2, "en", link.Code)
	require.NoError(t, err)
	require.Equal(t, "en", lang)
	_, _, err = store.UpsertUserWithRef(ctx, 1, "en", link.Code)
	require.NoError(t, err)
	// An unknown code is a plain /start.
	_, _, err = store.UpsertUserWithRef(ctx, 3, "en", "nope")
	require.NoError(t, err)

	got, err := store.RefLink(ctx, link.ID)
	require.NoError(t, err)
	require.Equal(t, 2, got.Starts)
	require.Equal(t, 1, got.Users)
	require.Zero(t, got.Connected)

	links, err := store.RefLinks(ctx)
	require.NoError(t, err)
	require.Len(t, links, 1)

	stats, err := store.AdminStats(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, stats.Users)
	require.Equal(t, 1, stats.FromRefs)
	require.Equal(t, 3, stats.New24h)

	require.NoError(t, store.DeleteRefLink(ctx, link.ID))
	stats, err = store.AdminStats(ctx)
	require.NoError(t, err)
	require.Zero(t, stats.FromRefs)
	require.Equal(t, 3, stats.Users)
}

func TestBroadcastLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	admin, _, err := store.UpsertUser(ctx, 10, "ru")
	require.NoError(t, err)
	other, _, err := store.UpsertUser(ctx, 11, "en")
	require.NoError(t, err)

	id, err := store.CreateBroadcast(ctx, admin, 10, 77)
	require.NoError(t, err)
	_, running, err := store.NextRunningBroadcast(ctx)
	require.NoError(t, err)
	require.False(t, running, "a draft is not sent until confirmed")

	require.NoError(t, store.StartBroadcast(ctx, id))
	require.NoError(t, store.StartBroadcast(ctx, id)) // double tap is a no-op
	bc, running, err := store.NextRunningBroadcast(ctx)
	require.NoError(t, err)
	require.True(t, running)
	require.Equal(t, 2, bc.Total)
	require.Equal(t, int64(10), bc.AdminTelegramID)

	targets, err := store.BroadcastTargets(ctx, 0, 1)
	require.NoError(t, err)
	require.Equal(t, []storage.BroadcastTarget{{UserID: admin, TelegramID: 10}}, targets)
	ok, err := store.AdvanceBroadcast(ctx, id, admin, 1, 0)
	require.NoError(t, err)
	require.True(t, ok)

	// A blocked user drops out of the audience until they come back.
	require.NoError(t, store.MarkUserBlocked(ctx, other))
	targets, err = store.BroadcastTargets(ctx, admin, 10)
	require.NoError(t, err)
	require.Empty(t, targets)
	n, err := store.BroadcastAudience(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	_, _, err = store.UpsertUser(ctx, 11, "en")
	require.NoError(t, err)
	n, err = store.BroadcastAudience(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)

	done, finished, err := store.FinishBroadcast(ctx, id)
	require.NoError(t, err)
	require.True(t, finished)
	require.Equal(t, storage.BroadcastDone, done.Status)
	require.Equal(t, 1, done.Sent)

	// Cancelling a finished broadcast changes nothing.
	require.NoError(t, store.CancelBroadcast(ctx, id))
	bc, err = store.Broadcast(ctx, id)
	require.NoError(t, err)
	require.Equal(t, storage.BroadcastDone, bc.Status)

	list, err := store.Broadcasts(ctx, 5)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestCancelStopsRunningBroadcast(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	admin, _, err := store.UpsertUser(ctx, 10, "ru")
	require.NoError(t, err)
	id, err := store.CreateBroadcast(ctx, admin, 10, 77)
	require.NoError(t, err)
	require.NoError(t, store.StartBroadcast(ctx, id))
	require.NoError(t, store.CancelBroadcast(ctx, id))

	ok, err := store.AdvanceBroadcast(ctx, id, admin, 1, 0)
	require.NoError(t, err)
	require.False(t, ok)
	_, finished, err := store.FinishBroadcast(ctx, id)
	require.NoError(t, err)
	require.False(t, finished)
}
