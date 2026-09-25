package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/domain"
	"github.com/faustyu/gh-notify-go/internal/storage"
)

func TestGitLabConnectionTokenRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	userID, _, _ := store.UpsertUser(ctx, 555, "en")

	id, err := store.CreateConnection(ctx, "gitlab", userID)
	require.NoError(t, err)

	token, err := store.WebhookToken(ctx, id, userID)
	require.NoError(t, err)
	require.Len(t, token, 64)

	found, err := store.InstallationByToken(ctx, "gitlab", token)
	require.NoError(t, err)
	require.Equal(t, id, found)

	// The token is stored sealed and hashed, never in the clear.
	var clear int
	require.NoError(t, store.Pool().QueryRow(ctx, `
		SELECT count(*) FROM installations
		WHERE position($1::bytea IN webhook_token_ciphertext) > 0`,
		[]byte(token)).Scan(&clear))
	require.Zero(t, clear)
}

func TestGitLabTokenIsOwnerOnly(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	owner, _, _ := store.UpsertUser(ctx, 1, "en")
	other, _, _ := store.UpsertUser(ctx, 2, "en")

	id, err := store.CreateConnection(ctx, "gitlab", owner)
	require.NoError(t, err)

	_, err = store.WebhookToken(ctx, id, other)
	require.Error(t, err)
}

func TestGitLabUnknownTokenIsRefused(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	_, err := store.InstallationByToken(ctx, "gitlab", "nope")
	require.ErrorIs(t, err, storage.ErrUnknownWebhookToken)
	_, err = store.InstallationByToken(ctx, "gitlab", "")
	require.ErrorIs(t, err, storage.ErrUnknownWebhookToken)
}

func TestGitLabConnectionForSetupReusesUnusedOne(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	userID, _, _ := store.UpsertUser(ctx, 555, "en")

	first, err := store.ConnectionForSetup(ctx, "gitlab", userID)
	require.NoError(t, err)
	again, err := store.ConnectionForSetup(ctx, "gitlab", userID)
	require.NoError(t, err)
	require.Equal(t, first, again, "an unused connection must be reused")

	require.NoError(t, store.RememberProject(ctx, first,
		storage.Project{ID: 15, Path: "mike/diaspora"}))
	next, err := store.ConnectionForSetup(ctx, "gitlab", userID)
	require.NoError(t, err)
	require.NotEqual(t, first, next, "a connection in use is not handed out again")
}

func TestRememberGitLabProjectNamesConnection(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	id, err := store.CreateConnection(ctx, "gitlab", userID)
	require.NoError(t, err)

	require.NoError(t, store.RememberProject(ctx, id, storage.Project{
		ID: 15, Path: "acme/group/diaspora", WebURL: "https://gitlab.example.com/acme/group/diaspora"}))
	require.NoError(t, store.RememberProject(ctx, id, storage.Project{
		ID: 16, Path: "other/thing"}))
	// A repeat updates the row instead of failing.
	require.NoError(t, store.RememberProject(ctx, id, storage.Project{
		ID: 15, Path: "acme/group/diaspora-renamed"}))

	installation, err := store.InstallationByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.ProviderGitLab, installation.Provider)
	require.Equal(t, "acme/group", installation.AccountLogin, "named after the first project only")

	projects, err := store.Projects(ctx, id, userID)
	require.NoError(t, err)
	require.Len(t, projects, 2)
	require.Equal(t, "acme/group/diaspora-renamed", projects[0].Path)

	other, _, _ := store.UpsertUser(ctx, 777, "en")
	projects, err = store.Projects(ctx, id, other)
	require.NoError(t, err)
	require.Empty(t, projects, "another user must not see the projects")
}

func TestGitLabIntegrationsDoNotCollideWithGitHub(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	userID, _, _ := store.UpsertUser(ctx, 555, "en")
	chatID, err := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
	require.NoError(t, err)

	require.NoError(t, store.ClaimInstallationOwner(ctx, 7, "acme", "Organization", userID))
	var githubInst int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT id FROM installations WHERE github_installation_id = 7`).Scan(&githubInst))
	gitlabInst, err := store.CreateConnection(ctx, "gitlab", userID)
	require.NoError(t, err)

	// Same numeric id on both sides, same chat: two different repositories.
	_, err = store.CreateIntegration(ctx, chatID, githubInst, 42, "acme/app", userID)
	require.NoError(t, err)
	glIntegration, err := store.CreateIntegration(ctx, chatID, gitlabInst, 42, "acme/app", userID)
	require.NoError(t, err)

	found, err := store.IntegrationsForProject(ctx, gitlabInst, 42)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, glIntegration, found[0].ID)
	require.Equal(t, domain.ProviderGitLab, found[0].Provider)

	provider, err := store.ProviderForIntegration(ctx, glIntegration)
	require.NoError(t, err)
	require.Equal(t, domain.ProviderGitLab, provider)

	github, err := store.IntegrationsForRepo(ctx, 42, 7)
	require.NoError(t, err)
	require.Len(t, github, 1)
	require.NotEqual(t, glIntegration, github[0].ID)

	// Listing a chat with a GitLab integration must not trip over the
	// missing GitHub installation id.
	inChat, err := store.IntegrationsInChat(ctx, chatID)
	require.NoError(t, err)
	require.Len(t, inChat, 2)
	installations, err := store.InstallationsForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, installations, 2)
}

func TestDeleteGitLabConnectionIsOwnerOnlyAndCascades(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	owner, _, _ := store.UpsertUser(ctx, 1, "en")
	other, _, _ := store.UpsertUser(ctx, 2, "en")
	chatID, err := store.UpsertChat(ctx, -100, "Team", "supergroup", false)
	require.NoError(t, err)

	id, err := store.CreateConnection(ctx, "gitlab", owner)
	require.NoError(t, err)
	_, err = store.CreateIntegration(ctx, chatID, id, 15, "mike/diaspora", owner)
	require.NoError(t, err)

	require.NoError(t, store.DeleteConnection(ctx, id, other))
	_, err = store.InstallationByID(ctx, id)
	require.NoError(t, err, "someone else's delete must be a no-op")

	require.NoError(t, store.DeleteConnection(ctx, id, owner))
	_, err = store.InstallationByID(ctx, id)
	require.Error(t, err)

	var left int
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM integrations`).Scan(&left))
	require.Zero(t, left)
}

func TestWebhookSecretBelongsToItsProvider(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	userID, _, _ := store.UpsertUser(ctx, 555, "en")

	id, err := store.CreateConnection(ctx, "gitea", userID)
	require.NoError(t, err)
	token, err := store.WebhookToken(ctx, id, userID)
	require.NoError(t, err)

	secret, err := store.WebhookSecret(ctx, "gitea", id)
	require.NoError(t, err)
	require.Equal(t, token, secret)

	// Another provider's URL, or a GitLab lookup of the same secret, finds
	// nothing.
	_, err = store.WebhookSecret(ctx, "forgejo", id)
	require.ErrorIs(t, err, storage.ErrUnknownWebhookToken)
	_, err = store.InstallationByToken(ctx, "gitlab", token)
	require.ErrorIs(t, err, storage.ErrUnknownWebhookToken)
}

func TestEveryWebhookProviderCanConnect(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	userID, _, _ := store.UpsertUser(ctx, 555, "en")

	for _, provider := range []string{"gitlab", "gitea", "forgejo", "gitverse"} {
		id, err := store.ConnectionForSetup(ctx, provider, userID)
		require.NoError(t, err, provider)
		installation, err := store.InstallationByID(ctx, id)
		require.NoError(t, err)
		require.Equal(t, provider, installation.Provider)
	}
	_, err := store.CreateConnection(ctx, "sourceforge", userID)
	require.Error(t, err, "the schema only admits known providers")
}
