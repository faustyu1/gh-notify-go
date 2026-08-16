package storage_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/secret"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
)

func newStore(t *testing.T) *storage.Store {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)

	box, err := secret.NewBox(base64.StdEncoding.EncodeToString(key))
	require.NoError(t, err)

	store, err := storage.New(context.Background(), testhelper.StartPostgres(t), box)
	require.NoError(t, err)
	t.Cleanup(store.Close)
	return store
}

func TestUpsertUserIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	first, _, err := store.UpsertUser(ctx, 555, "en")
	require.NoError(t, err)
	second, _, err := store.UpsertUser(ctx, 555, "en")
	require.NoError(t, err)
	require.Equal(t, first, second)
}

// The Telegram client language seeds the row once; afterwards only an
// explicit choice from the settings screen may change it, so a user who
// picked Russian keeps Russian even with an English client.
func TestUpsertUserKeepsExplicitLanguage(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	_, lang, err := store.UpsertUser(ctx, 555, "ru")
	require.NoError(t, err)
	require.Equal(t, "ru", lang)

	require.NoError(t, store.SetUserLanguage(ctx, mustUserID(t, store, 555), "de"))

	_, lang, err = store.UpsertUser(ctx, 555, "en")
	require.NoError(t, err)
	require.Equal(t, "de", lang, "a later language_code must not win")
}

func mustUserID(t *testing.T, store *storage.Store, telegramID int64) int64 {
	t.Helper()
	var id int64
	require.NoError(t, store.Pool().QueryRow(context.Background(),
		`SELECT id FROM users WHERE telegram_id = $1`, telegramID).Scan(&id))
	return id
}

func TestUpsertChatRefreshesTitle(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	id, err := store.UpsertChat(ctx, -100, "Old name", "supergroup")
	require.NoError(t, err)
	again, err := store.UpsertChat(ctx, -100, "New name", "supergroup")
	require.NoError(t, err)
	require.Equal(t, id, again)

	var title string
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT title FROM chats WHERE id = $1`, id).Scan(&title))
	require.Equal(t, "New name", title)
}

func TestIntegrationsForRepoJoinsChatAndOwner(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, err := store.UpsertUser(ctx, 555, "en")
	require.NoError(t, err)
	chatID, err := store.UpsertChat(ctx, -100, "Team", "supergroup")
	require.NoError(t, err)
	installID := mustInstallation(t, store, 7, "acme", "Organization", userID)
	_, err = store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	require.NoError(t, err)

	found, err := store.IntegrationsForRepo(ctx, 42, 7)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, int64(-100), found[0].TelegramChatID)
	require.Equal(t, "acme/app", found[0].RepoFullName)
	require.Equal(t, int64(555), found[0].OwnerTelegramID)
	require.Nil(t, found[0].TopicID)
}

func TestIntegrationsForRepoSkipsMutedChats(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	installID := mustInstallation(t, store, 7, "acme", "Organization", userID)
	_, err := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)
	require.NoError(t, err)

	_, err = store.Pool().Exec(ctx,
		`UPDATE chats SET muted_until = now() + interval '1 hour' WHERE id = $1`, chatID)
	require.NoError(t, err)

	found, err := store.IntegrationsForRepo(ctx, 42, 7)
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestEventEnabledDefaultsToTrueWithNoRow(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	chatID, _ := store.UpsertChat(ctx, -100, "Team", "supergroup")
	installID := mustInstallation(t, store, 7, "acme", "Organization", userID)
	integrationID, _ := store.CreateIntegration(ctx, chatID, installID, 42, "acme/app", userID)

	// No event_settings row exists yet: a new integration is fully on.
	enabled, err := store.EventEnabled(ctx, integrationID, "push")
	require.NoError(t, err)
	require.True(t, enabled)

	_, err = store.Pool().Exec(ctx,
		`INSERT INTO event_settings (integration_id, event_kind, enabled)
		 VALUES ($1, 'push', false)`, integrationID)
	require.NoError(t, err)

	enabled, err = store.EventEnabled(ctx, integrationID, "push")
	require.NoError(t, err)
	require.False(t, enabled)
}

func TestTokenCacheRoundTripsEncrypted(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	mustInstallation(t, store, 7, "acme", "Organization", userID)

	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	cache := store.TokenCache()
	require.NoError(t, cache.Put(ctx, 7, "ghs_secret_value", expires))

	// The plaintext must not be readable straight out of the column.
	var stored []byte
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT token_ciphertext FROM installations
		 WHERE github_installation_id = 7`).Scan(&stored))
	require.NotContains(t, string(stored), "ghs_secret_value")

	token, gotExpires, err := cache.Get(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, "ghs_secret_value", token)
	require.Equal(t, expires, gotExpires.UTC().Truncate(time.Second))
}

func TestTokenCacheGetReturnsEmptyWhenAbsent(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	token, _, err := store.TokenCache().Get(ctx, 999)
	require.NoError(t, err)
	require.Empty(t, token)
}

// mustInstallation creates an installation the way production does — through
// the claim path — and returns its internal id, which most tests need in
// order to hang an integration off it.
func mustInstallation(
	t *testing.T, store *storage.Store, githubID int64, login, accountType string, userID int64,
) int64 {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, store.ClaimInstallationOwner(ctx, githubID, login, accountType, userID))

	var id int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT id FROM installations WHERE github_installation_id = $1`, githubID).Scan(&id))
	return id
}
