package screens_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/faustyu/gh-notify-go/internal/domain"
	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
	"github.com/faustyu/gh-notify-go/internal/tg/ui/screens"
)

// fakeStore satisfies screens.Store without a database, so screen layout is
// tested in milliseconds rather than against a container.
type fakeStore struct {
	accounts, repos, chats int
	installations          []domain.Installation
	installation           domain.Installation
	chatList               []domain.ChatSummary
	chat                   domain.Chat
	chatIntegrations       []domain.Integration
	eventSettings          map[string]bool
	filters                []storage.Filter
	sent, failed           int
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

func (f *fakeStore) ChatsForUser(context.Context, int64) ([]domain.ChatSummary, error) {
	return f.chatList, nil
}

func (f *fakeStore) ChatByTelegramID(_ context.Context, _ int64) (domain.Chat, error) {
	return f.chat, nil
}

func (f *fakeStore) IntegrationsInChat(context.Context, int64) ([]domain.Integration, error) {
	return f.chatIntegrations, nil
}

func (f *fakeStore) EventSettings(context.Context, int64) (map[string]bool, error) {
	return f.eventSettings, nil
}

func (f *fakeStore) FiltersForIntegration(context.Context, int64) ([]storage.Filter, error) {
	return f.filters, nil
}

func (f *fakeStore) StatusStats(context.Context, int64) (int, int, error) {
	return f.sent, f.failed, nil
}

// fakeStates records who the install token was minted for.
type fakeStates struct {
	token     string
	mintedFor int64
}

func (f *fakeStates) NewInstallState(
	_ context.Context, userID int64, _ time.Duration,
) (string, error) {
	f.mintedFor = userID
	return f.token, nil
}

type fakeRepos struct{ list []ghapp.Repository }

func (f fakeRepos) ListRepositories(context.Context, int64) ([]ghapp.Repository, error) {
	return f.list, nil
}

var loc = i18n.MustNewBundle()

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
	screen := screens.NewHome(&fakeStore{}, loc)

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 1})
	require.NoError(t, err)
	require.Contains(t, view.Text, "tg-emoji")
	require.Contains(t, labels(view), "🔗 Connect GitHub")
	require.NotContains(t, labels(view), "🏢 Repositories")
}

func TestHomeRendersInUserLanguage(t *testing.T) {
	screen := screens.NewHome(&fakeStore{}, loc)

	ru, err := screen.Render(context.Background(),
		ui.Session{UserID: 1, Depth: 1, Lang: "ru"})
	require.NoError(t, err)
	require.Contains(t, ru.Text, "Подключи GitHub")

	de, err := screen.Render(context.Background(),
		ui.Session{UserID: 1, Depth: 1, Lang: "de"})
	require.NoError(t, err)
	require.Contains(t, de.Text, "Verbinde GitHub")
}

func TestHomeWithInstallationShowsCounts(t *testing.T) {
	screen := screens.NewHome(&fakeStore{accounts: 2, repos: 5, chats: 3}, loc)

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 1})
	require.NoError(t, err)
	require.Contains(t, view.Text, "2")
	require.Contains(t, view.Text, "5")
	require.Contains(t, view.Text, "3")
	require.Contains(t, labels(view), "🏢 Repositories")
	require.Contains(t, labels(view), "💬 Chats")
}

func TestInstallScreenLinksToGitHubWithSingleUseState(t *testing.T) {
	states := &fakeStates{token: "s3cr3t-token"}
	screen := screens.NewInstall("gh-notify", "https://bot.example.com", states, loc)

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
	require.Contains(t, url, "state=s3cr3t-token")
	// The user id must not be what the redirect carries: it is guessable,
	// and the setup endpoint is unauthenticated.
	require.NotContains(t, url, "state=42")
	require.Equal(t, int64(42), states.mintedFor)
}

func TestAccountsListsEachInstallation(t *testing.T) {
	screen := screens.NewAccounts(&fakeStore{installations: []domain.Installation{
		{ID: 1, AccountLogin: "acme", AccountType: "Organization"},
		{ID: 2, AccountLogin: "octocat", AccountType: "User", Suspended: true},
	}}, loc)

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
	screen := screens.NewRepos(&fakeStore{}, fakeRepos{list: list}, 10, loc)

	view, err := screen.Render(context.Background(),
		ui.Session{UserID: 1, Depth: 3, Params: ui.Params{"installation": "1"}})
	require.NoError(t, err)

	require.Contains(t, labels(view), "Older ▷")
	require.NotContains(t, labels(view), "◁ Back ")

	page2, err := screen.Render(context.Background(),
		ui.Session{UserID: 1, Depth: 3, Params: ui.Params{"installation": "1", "page": "1"}})
	require.NoError(t, err)
	require.Contains(t, page2.Text, "2/3")
}

func TestReposWithNoRepositoriesExplainsWhy(t *testing.T) {
	screen := screens.NewRepos(&fakeStore{}, fakeRepos{}, 10, loc)

	view, err := screen.Render(context.Background(),
		ui.Session{UserID: 1, Depth: 3, Params: ui.Params{"installation": "1"}})
	require.NoError(t, err)
	require.Contains(t, view.Text, "No repositories are selected")
}

func TestPlaceholderRendersTitleAndWayBack(t *testing.T) {
	screen := screens.NewPlaceholder("status", "Status", loc)

	require.Equal(t, "status", screen.Name())

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 2})
	require.NoError(t, err)
	require.Contains(t, view.Text, "Status")
	require.Contains(t, labels(view), "🏠 Home")
}

func TestAddToChatLinksToTelegramGroupPicker(t *testing.T) {
	screen := screens.NewAddToChat("g0thubbot", loc)

	require.Equal(t, "add_to_chat", screen.Name())

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 2})
	require.NoError(t, err)
	require.Contains(t, view.Text, "Add to chat")
	require.Contains(t, labels(view), "➕ Add to group")

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

type fakeChats struct{ list []domain.ChatSummary }

func (f *fakeChats) CandidateChatsForUser(
	context.Context, int64,
) ([]domain.ChatSummary, error) {
	return f.list, nil
}

func TestChatPickerOffersANewChatAlongsideExistingOnes(t *testing.T) {
	// Listing the chats the bot already sits in is not enough: connecting a
	// repository to a group the bot has never joined has to start here too.
	screen := screens.NewChatPicker(&fakeChats{list: []domain.ChatSummary{
		{ChatID: 1, TelegramChatID: -100, Title: "Team"},
	}}, loc)

	view, err := screen.Render(context.Background(), ui.Session{
		UserID: 1, Depth: 2,
		Params: ui.Params{"installation": "5", "repo": "7", "name": "octocat/hello"},
	})
	require.NoError(t, err)
	require.Contains(t, labels(view), "💬 Team")
	require.Contains(t, labels(view), "➕ Add to chat")

	last := view.Rows[len(view.Rows)-1][0]
	require.Equal(t, "add_to_chat", last.Screen, "the way in comes after the chats")
}

func TestChatPickerWithNoChatsOffersTheWayIn(t *testing.T) {
	screen := screens.NewChatPicker(&fakeChats{}, loc)

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 2})
	require.NoError(t, err)
	require.Contains(t, labels(view), "➕ Add to chat")
}

func TestChatsListsChatsWithCounts(t *testing.T) {
	screen := screens.NewChats(&fakeStore{chatList: []domain.ChatSummary{
		{ChatID: 1, TelegramChatID: -100, Title: "Team", IntegrationCount: 2},
	}}, loc)

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 1})
	require.NoError(t, err)
	require.Contains(t, labels(view), "💬 Team · 2")

	button := view.Rows[0][0]
	require.Equal(t, "chat_detail", button.Screen)
	require.Equal(t, "-100", button.Params["chat"])
}

func TestChatDetailShowsMuteAndIntegrations(t *testing.T) {
	screen := screens.NewChatDetail(&fakeStore{
		chat: domain.Chat{ID: 1, TelegramChatID: -100, Title: "Team", Kind: "supergroup"},
		chatIntegrations: []domain.Integration{
			{ID: 7, RepoFullName: "acme/app"},
		},
	}, loc)

	view, err := screen.Render(context.Background(),
		ui.Session{UserID: 1, Depth: 2, Params: ui.Params{"chat": "-100"}})
	require.NoError(t, err)
	require.Contains(t, view.Text, "Team")
	require.Contains(t, view.Text, "Notifications are on")
	require.Contains(t, labels(view), "📂 acme/app")
	require.Contains(t, labels(view), "🔇 1h")
	require.Contains(t, labels(view), "🏷 Set topic")
}

func TestEventsScreenDefaultsOnAndShowsExplicitOff(t *testing.T) {
	screen := screens.NewEvents(&fakeStore{
		eventSettings: map[string]bool{"push": false},
	}, loc)

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 2,
		Params: ui.Params{"integration": "5", "name": "acme/app"}})
	require.NoError(t, err)

	all := labels(view)
	require.Contains(t, all, "✅ Everything")
	require.Contains(t, all, "❌ Nothing")
	require.Contains(t, all, "❌ push", "an explicit off row must be off")
	require.Contains(t, all, "✅ pull_request", "a kind without a row defaults to on")

	var toggle string
	for _, row := range view.Rows {
		for _, b := range row {
			if b.Screen == "a_ev_toggle" && b.Params["kind"] == "push" {
				toggle = b.Params["to"]
			}
		}
	}
	require.Equal(t, "1", toggle, "tapping an off toggle must turn it on")
}

func TestFiltersScreenListsRulesAndAddButtons(t *testing.T) {
	screen := screens.NewFilters(&fakeStore{
		filters: []storage.Filter{{ID: 3, Kind: "author", Value: "dependabot*"}},
	}, loc)

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 2,
		Params: ui.Params{"integration": "5", "name": "acme/app"}})
	require.NoError(t, err)

	require.Contains(t, labels(view), "✖ 👤 Author: dependabot*")
	require.Contains(t, labels(view), "+ 🌿 Branch")

	var del *ui.Button
	for _, row := range view.Rows {
		for _, b := range row {
			if b.Screen == "a_filter_del" {
				del = &b
			}
		}
	}
	require.NotNil(t, del)
	require.Equal(t, "5", del.Params["integration"])
}

func TestStatusScreenSummarises(t *testing.T) {
	screen := screens.NewStatus(&fakeStore{accounts: 1, repos: 2, chats: 1, sent: 12, failed: 1}, loc)

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 1})
	require.NoError(t, err)
	require.Contains(t, view.Text, "12")
	require.Contains(t, view.Text, "Not delivered: <b>1</b>")
}

func TestSettingsOffersEveryLanguageAndMarksCurrent(t *testing.T) {
	screen := screens.NewSettings(20, loc)

	view, err := screen.Render(context.Background(),
		ui.Session{UserID: 1, Depth: 1, Lang: "ru"})
	require.NoError(t, err)

	all := labels(view)
	require.Contains(t, all, "● Русский", "the active language is marked")
	require.Contains(t, all, "English")
	require.Contains(t, all, "Español")
	require.Contains(t, all, "Deutsch")
	require.Contains(t, all, "Português")

	var en *ui.Button
	for _, row := range view.Rows {
		for i := range row {
			if row[i].Screen == "a_user_lang" && row[i].Params["lang"] == "en" {
				en = &row[i]
			}
		}
	}
	require.NotNil(t, en)
	require.Equal(t, "English", en.Label)
}

func TestHealthScreenShowsVitals(t *testing.T) {
	screen := screens.NewHealth(fakeHealth{broken: "bot was kicked", sent: 5}, loc)

	view, err := screen.Render(context.Background(), ui.Session{UserID: 1, Depth: 2,
		Params: ui.Params{"integration": "7"}})
	require.NoError(t, err)
	require.Contains(t, view.Text, "Broken:")
	require.Contains(t, view.Text, "5")
}

type fakeHealth struct {
	broken string
	sent   int
}

func (f fakeHealth) HealthForIntegration(context.Context, int64) (storage.IntegrationHealth, error) {
	h := storage.IntegrationHealth{
		RepoFullName: "acme/app", ChatTitle: "Team", Sent24h: f.sent,
	}
	if f.broken != "" {
		h.BrokenReason = &f.broken
	}
	return h, nil
}
