package screens_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/domain"
	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
	"github.com/faustyu/gh-notify-go/internal/tg/ui/screens"
)

// fakeStore satisfies screens.Store without a database, so screen layout is
// tested in milliseconds rather than against a container.
type fakeStore struct {
	accounts, repos, chats int
	installations          []domain.Installation
	installation           domain.Installation
}

func (f *fakeStore) CountsForUser(context.Context, int64) (int, int, int, error) {
	return f.accounts, f.repos, f.chats, nil
}

func (f *fakeStore) InstallationsForUser(context.Context, int64) ([]domain.Installation, error) {
	return f.installations, nil
}

func (f *fakeStore) InstallationByID(context.Context, int64) (domain.Installation, error) {
	return f.installation, nil
}

type fakeRepos struct{ list []ghapp.Repository }

func (f fakeRepos) ListRepositories(context.Context, int64) ([]ghapp.Repository, error) {
	return f.list, nil
}

func labels(view ui.View) []string {
	var out []string
	for _, row := range view.Rows {
		for _, b := range row {
			out = append(out, b.Label)
		}
	}
	return out
}

func TestHomeWithNoInstallationOffersInstall(t *testing.T) {
	screen := screens.NewHome(&fakeStore{})

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 1})
	require.NoError(t, err)
	require.Contains(t, view.Text, "tg-emoji")
	require.Contains(t, labels(view), "🔗 Подключить GitHub")
	require.NotContains(t, labels(view), "🏢 Репозитории")
}

func TestHomeWithInstallationShowsCounts(t *testing.T) {
	screen := screens.NewHome(&fakeStore{accounts: 2, repos: 5, chats: 3})

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 1})
	require.NoError(t, err)
	require.Contains(t, view.Text, "2")
	require.Contains(t, view.Text, "5")
	require.Contains(t, view.Text, "3")
	require.Contains(t, labels(view), "🏢 Репозитории")
	require.Contains(t, labels(view), "💬 Чаты")
}

func TestInstallScreenLinksToGitHubWithState(t *testing.T) {
	screen := screens.NewInstall("gh-notify", "https://bot.example.com")

	view, err := screen.Render(context.Background(), ui.Session{UserID: 42, Depth: 2})
	require.NoError(t, err)

	var url string
	for _, row := range view.Rows {
		for _, b := range row {
			if b.URL != "" {
				url = b.URL
			}
		}
	}
	require.True(t, strings.HasPrefix(url, "https://github.com/apps/gh-notify/installations/new"))
	require.Contains(t, url, "state=42")
}

func TestAccountsListsEachInstallation(t *testing.T) {
	screen := screens.NewAccounts(&fakeStore{installations: []domain.Installation{
		{ID: 1, AccountLogin: "acme", AccountType: "Organization"},
		{ID: 2, AccountLogin: "octocat", AccountType: "User", Suspended: true},
	}})

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 2})
	require.NoError(t, err)
	require.Contains(t, labels(view), "🏢 acme")
	// A suspended installation must be visibly different, not silently listed.
	require.Contains(t, labels(view), "⚠️ octocat")
}

func TestReposPaginates(t *testing.T) {
	list := make([]ghapp.Repository, 0, 25)
	for i := range 25 {
		list = append(list, ghapp.Repository{
			GitHubID: int64(i), FullName: "acme/repo" + string(rune('a'+i)),
		})
	}
	screen := screens.NewRepos(&fakeStore{}, fakeRepos{list: list}, 10)

	view, err := screen.Render(context.Background(),
		ui.Session{UserID: 1, Depth: 3, Params: ui.Params{"installation": "1"}})
	require.NoError(t, err)

	require.Contains(t, labels(view), "Вперёд ▷")
	require.NotContains(t, labels(view), "◁ Назад ")

	page2, err := screen.Render(context.Background(),
		ui.Session{UserID: 1, Depth: 3, Params: ui.Params{"installation": "1", "page": "1"}})
	require.NoError(t, err)
	require.Contains(t, page2.Text, "2/3")
}

func TestReposWithNoRepositoriesExplainsWhy(t *testing.T) {
	screen := screens.NewRepos(&fakeStore{}, fakeRepos{}, 10)

	view, err := screen.Render(context.Background(),
		ui.Session{UserID: 1, Depth: 3, Params: ui.Params{"installation": "1"}})
	require.NoError(t, err)
	require.Contains(t, view.Text, "Не выбрано ни одного репозитория")
}

func TestPlaceholderRendersTitleAndWayBack(t *testing.T) {
	screen := screens.NewPlaceholder("status", "Статус")

	require.Equal(t, "status", screen.Name())

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 2})
	require.NoError(t, err)
	require.Contains(t, view.Text, "Статус")
	require.Contains(t, labels(view), "🏠 В начало")
}

func TestAddToChatLinksToTelegramGroupPicker(t *testing.T) {
	screen := screens.NewAddToChat("g0thubbot")

	require.Equal(t, "add_to_chat", screen.Name())

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 2})
	require.NoError(t, err)
	require.Contains(t, view.Text, "Добавить в чат")
	require.Contains(t, labels(view), "➕ Добавить в группу")

	var url string
	for _, row := range view.Rows {
		for _, b := range row {
			if b.URL != "" {
				url = b.URL
			}
		}
	}
	require.Equal(t, "https://t.me/g0thubbot?startgroup=add", url)
}
