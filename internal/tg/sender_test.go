package tg_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegoapi"
	"github.com/stretchr/testify/require"

	_ "github.com/faustyu/gh-notify-go/internal/events" // register event kinds
	"github.com/faustyu/gh-notify-go/internal/outbox"
	"github.com/faustyu/gh-notify-go/internal/tg"
)

// fakeAPI records every SendMessage call and replays a scripted error queue.
type fakeAPI struct {
	mu     sync.Mutex
	sent   []*telego.SendMessageParams
	errors []error
}

func (f *fakeAPI) SendMessage(
	_ context.Context, params *telego.SendMessageParams,
) (*telego.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.sent = append(f.sent, params)
	if len(f.errors) > 0 {
		err := f.errors[0]
		f.errors = f.errors[1:]
		return nil, err
	}
	return &telego.Message{MessageID: len(f.sent)}, nil
}

func pushJob(t *testing.T) outbox.Job {
	t.Helper()
	return outbox.Job{
		ID: 1, IntegrationID: 1, TelegramChatID: -100, Kind: "push",
		Payload: json.RawMessage(`{
			"ref":"refs/heads/main",
			"repository":{"full_name":"acme/app"},
			"sender":{"login":"octocat","html_url":"https://github.com/octocat"},
			"commits":[{"id":"bbbbbbbb","message":"fix: x","url":"https://x/1"}]
		}`),
	}
}

func TestDeliverSendsRenderedHTML(t *testing.T) {
	api := &fakeAPI{}
	sender := tg.NewSender(api, nil)

	require.NoError(t, sender.Deliver(context.Background(), pushJob(t)))
	require.Len(t, api.sent, 1)

	sent := api.sent[0]
	require.Equal(t, telego.ModeHTML, sent.ParseMode)
	require.Contains(t, sent.Text, "acme/app")
	require.Contains(t, sent.Text, "tg-emoji")
	require.True(t, sent.LinkPreviewOptions.IsDisabled)
}

func TestDeliverRoutesToForumTopic(t *testing.T) {
	api := &fakeAPI{}
	sender := tg.NewSender(api, nil)

	topic := int64(77)
	job := pushJob(t)
	job.TopicID = &topic

	require.NoError(t, sender.Deliver(context.Background(), job))
	require.Equal(t, 77, api.sent[0].MessageThreadID)
}

func TestDeliverRetriesWithoutTopicWhenTopicIsGone(t *testing.T) {
	api := &fakeAPI{errors: []error{&telegoapi.Error{
		ErrorCode:   400,
		Description: "Bad Request: message thread not found",
	}}}

	var clearedChat int64
	sender := tg.NewSender(api, func(_ context.Context, chatID int64) error {
		clearedChat = chatID
		return nil
	})

	topic := int64(77)
	job := pushJob(t)
	job.TopicID = &topic

	require.NoError(t, sender.Deliver(context.Background(), job))
	require.Len(t, api.sent, 2)
	require.Equal(t, 77, api.sent[0].MessageThreadID)
	require.Zero(t, api.sent[1].MessageThreadID, "retry must drop the thread id")
	require.Equal(t, int64(-100), clearedChat, "the dead topic must be cleared")
}

func TestDeliverRetriesWithoutCustomEmojiWhenRejected(t *testing.T) {
	api := &fakeAPI{errors: []error{&telegoapi.Error{
		ErrorCode:   400,
		Description: "Bad Request: can't parse entities: unsupported custom emoji",
	}}}
	sender := tg.NewSender(api, nil)

	require.NoError(t, sender.Deliver(context.Background(), pushJob(t)))
	require.Len(t, api.sent, 2)
	require.Contains(t, api.sent[0].Text, "tg-emoji")
	require.NotContains(t, api.sent[1].Text, "tg-emoji")
	require.Contains(t, api.sent[1].Text, "⬆", "the unicode fallback must survive")
}

func TestDeliverReportsKickedAsPermanent(t *testing.T) {
	api := &fakeAPI{errors: []error{&telegoapi.Error{
		ErrorCode:   403,
		Description: "Forbidden: bot was kicked from the supergroup chat",
	}}}
	sender := tg.NewSender(api, nil)

	err := sender.Deliver(context.Background(), pushJob(t))
	require.ErrorIs(t, err, outbox.ErrPermanent)
}

func TestDeliverTreatsUnknownEventAsPermanent(t *testing.T) {
	api := &fakeAPI{}
	sender := tg.NewSender(api, nil)

	job := pushJob(t)
	job.Kind = "not_a_real_event"

	err := sender.Deliver(context.Background(), job)
	require.ErrorIs(t, err, outbox.ErrPermanent)
	require.Empty(t, api.sent)
}

func TestDeliverSplitsOverlongMessages(t *testing.T) {
	api := &fakeAPI{}
	sender := tg.NewSender(api, nil)

	commits := make([]string, 0, 400)
	for i := range 400 {
		commits = append(commits, `{"id":"aaaaaaaa","message":"commit `+
			strings.Repeat("x", 40)+string(rune('a'+i%26))+`","url":"https://x/1"}`)
	}
	job := pushJob(t)
	job.Payload = json.RawMessage(`{
		"ref":"refs/heads/main",
		"repository":{"full_name":"acme/app"},
		"sender":{"login":"octocat","html_url":"https://github.com/octocat"},
		"commits":[` + strings.Join(commits, ",") + `]}`)

	require.NoError(t, sender.Deliver(context.Background(), job))
	for _, sent := range api.sent {
		require.LessOrEqual(t, len([]rune(sent.Text)), 4096)
	}
}

func TestSplitBreaksOnLineBoundaries(t *testing.T) {
	body := strings.Repeat("line of text\n", 500)
	parts := tg.Split(body, 200)

	require.Greater(t, len(parts), 1)
	for _, p := range parts {
		require.LessOrEqual(t, len([]rune(p)), 200)
	}
	require.Equal(t, strings.TrimSpace(body), strings.TrimSpace(strings.Join(parts, "\n")))
}

func TestSplitKeepsShortTextIntact(t *testing.T) {
	require.Equal(t, []string{"short"}, tg.Split("short", 4096))
}

func TestClassifyErrorReadsRetryAfter(t *testing.T) {
	permanent, retryAfter := tg.ClassifyError(&telegoapi.Error{
		ErrorCode:  429,
		Parameters: &telegoapi.ResponseParameters{RetryAfter: 12},
	})
	require.False(t, permanent)
	require.Equal(t, 12*time.Second, retryAfter)
}

func TestClassifyErrorTreatsNetworkErrorsAsTransient(t *testing.T) {
	permanent, retryAfter := tg.ClassifyError(errors.New("connection reset"))
	require.False(t, permanent)
	require.Zero(t, retryAfter)
}
