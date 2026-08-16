package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/secret"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
)

// fakeAdmin answers the admin question without calling Telegram.
type fakeAdmin struct{ allow bool }

func (f fakeAdmin) IsAdmin(context.Context, int64, int64) (bool, error) {
	return f.allow, nil
}

func newIntegrator(t *testing.T, allow bool) (*service.Integrator, *storage.Store, service.ConnectRequest) {
	t.Helper()
	ctx := context.Background()

	box, err := secret.NewBox("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	require.NoError(t, err)
	store, err := storage.New(ctx, testhelper.StartPostgres(t), box)
	require.NoError(t, err)
	t.Cleanup(store.Close)

	userID, err := store.UpsertUser(ctx, 555)
	require.NoError(t, err)
	chatID, err := store.UpsertChat(ctx, -100, "Team", "supergroup")
	require.NoError(t, err)
	installID, err := store.UpsertInstallation(ctx, 7, "acme", "Organization", userID)
	require.NoError(t, err)

	req := service.ConnectRequest{
		UserID: userID, TelegramUserID: 555,
		InstallationID: installID,
		ChatID:         chatID, TelegramChatID: -100,
		RepoGitHubID: 42, RepoFullName: "acme/app",
	}
	return service.NewIntegrator(store, fakeAdmin{allow: allow}), store, req
}

func TestConnectCreatesIntegration(t *testing.T) {
	ctx := context.Background()
	integrator, store, req := newIntegrator(t, true)

	require.NoError(t, integrator.Connect(ctx, req))

	found, err := store.IntegrationsForRepo(ctx, 42, 7)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "acme/app", found[0].RepoFullName)
}

func TestConnectRecordsAudit(t *testing.T) {
	ctx := context.Background()
	integrator, store, req := newIntegrator(t, true)

	require.NoError(t, integrator.Connect(ctx, req))

	var action string
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT action FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&action))
	require.Equal(t, "integration.create", action)
}

func TestConnectRefusesNonAdmin(t *testing.T) {
	ctx := context.Background()
	integrator, store, req := newIntegrator(t, false)

	err := integrator.Connect(ctx, req)
	require.ErrorIs(t, err, service.ErrNotAdmin)

	found, err := store.IntegrationsForRepo(ctx, 42, 7)
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestConnectRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	integrator, _, req := newIntegrator(t, true)

	require.NoError(t, integrator.Connect(ctx, req))
	require.ErrorIs(t, integrator.Connect(ctx, req), service.ErrAlreadyConnected)
}
