package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/secret"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/storage/testhelper"
)

type fakeInstallationInfo struct {
	account ghapp.Account
	err     error
}

func (f fakeInstallationInfo) InstallationInfo(context.Context, int64) (ghapp.Account, error) {
	return f.account, f.err
}

func installationEnvelope(action, delivery string) ghapp.Envelope {
	return envelope("installation", action, delivery, `{
		"action": "`+action+`",
		"installation": {"id": 154234486,
			"account": {"login": "faustyu1", "type": "User"}},
		"repositories": [{"full_name": "faustyu1/rd-132211"}]
	}`)
}

func TestHandleInstallationCreatedRegistersRow(t *testing.T) {
	ctx := context.Background()
	ingest, pool, _ := newIngest(t)

	result, err := ingest.Handle(ctx, installationEnvelope("created", "i-1"))
	require.NoError(t, err)
	require.False(t, result.Duplicate)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM installations WHERE github_installation_id = 154234486`).
		Scan(&count))
	require.Equal(t, 1, count)

	// GitHub retries the delivery: no second row, flagged as duplicate.
	result, err = ingest.Handle(ctx, installationEnvelope("created", "i-1"))
	require.NoError(t, err)
	require.True(t, result.Duplicate)
}

func TestHandleInstallationDeletedRemovesRow(t *testing.T) {
	ctx := context.Background()
	ingest, pool, _ := newIngest(t)

	_, err := ingest.Handle(ctx, installationEnvelope("created", "i-1"))
	require.NoError(t, err)

	_, err = ingest.Handle(ctx, installationEnvelope("deleted", "i-2"))
	require.NoError(t, err)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM installations WHERE github_installation_id = 154234486`).
		Scan(&count))
	require.Zero(t, count)
}

func TestClaimInstallationAssignsOwner(t *testing.T) {
	ctx := context.Background()

	box, err := secret.NewBox("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	require.NoError(t, err)
	store, err := storage.New(ctx, testhelper.StartPostgres(t), box)
	require.NoError(t, err)
	t.Cleanup(store.Close)

	// The webhook row exists but is ownerless until the redirect claims it.
	require.NoError(t, store.RegisterInstallation(ctx, 154234486, "faustyu1", "User"))

	installations := service.NewInstallations(store,
		fakeInstallationInfo{account: ghapp.Account{Login: "faustyu1", Type: "User"}})

	userID, _, err := store.UpsertUser(ctx, 555, "en")
	require.NoError(t, err)
	require.NoError(t, installations.ClaimInstallation(ctx, 154234486, userID))

	found, err := store.InstallationsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "faustyu1", found[0].AccountLogin)
	require.False(t, found[0].Suspended)
}
