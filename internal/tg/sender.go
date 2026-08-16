// Package tg turns outbox jobs into Telegram messages. It owns every
// Telegram-specific failure mode so the worker only has to know "retry" or
// "give up".
package tg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegoapi"

	"github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/outbox"
)

// messageLimit is Telegram's hard cap on message length in characters.
const messageLimit = 4096

// ErrKicked marks a 403: the bot was kicked, blocked, or cannot write to the
// chat. Waiting will not help, and the integration should be marked broken.
var ErrKicked = errors.New("bot cannot write to this chat")

type API interface {
	SendMessage(ctx context.Context, params *telego.SendMessageParams) (*telego.Message, error)
}

type Sender struct {
	api API

	// onTopicMissing clears a forum topic that Telegram says no longer
	// exists, so later events go to the General topic instead of failing.
	onTopicMissing func(ctx context.Context, chatID int64) error
}

func NewSender(api API, onTopicMissing func(ctx context.Context, chatID int64) error) *Sender {
	return &Sender{api: api, onTopicMissing: onTopicMissing}
}

func (s *Sender) Deliver(ctx context.Context, job outbox.Job) error {
	html, err := events.Render(events.Kind(job.Kind), job.Payload)
	if err != nil {
		// An unrenderable payload will not become renderable later.
		return fmt.Errorf("%w: render %s: %v", outbox.ErrPermanent, job.Kind, err)
	}

	for _, part := range Split(html, messageLimit) {
		if err := s.sendPart(ctx, job, part); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sender) sendPart(ctx context.Context, job outbox.Job, text string) error {
	params := &telego.SendMessageParams{
		ChatID:             telego.ChatID{ID: job.TelegramChatID},
		Text:               text,
		ParseMode:          telego.ModeHTML,
		LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true},
	}
	if job.TopicID != nil {
		params.MessageThreadID = int(*job.TopicID)
	}

	_, err := s.api.SendMessage(ctx, params)
	if err == nil {
		return nil
	}

	// The topic was deleted. Clear it and resend to the General topic.
	if isTopicMissing(err) && job.TopicID != nil {
		if s.onTopicMissing != nil {
			if clearErr := s.onTopicMissing(ctx, job.TelegramChatID); clearErr != nil {
				return fmt.Errorf("clear missing topic: %w", clearErr)
			}
		}
		retry := *params
		retry.MessageThreadID = 0
		if _, retryErr := s.api.SendMessage(ctx, &retry); retryErr != nil {
			return classify(retryErr)
		}
		return nil
	}

	// Custom emoji were rejected. Resend once with the tags stripped, so the
	// message still arrives looking plain rather than not arriving at all.
	if isCustomEmojiRejected(err) {
		retry := *params
		retry.Text = render.Strip(params.Text)
		if _, retryErr := s.api.SendMessage(ctx, &retry); retryErr != nil {
			return classify(retryErr)
		}
		return nil
	}

	return classify(err)
}

func classify(err error) error {
	permanent, retryAfter := ClassifyError(err)
	if permanent {
		wrapped := fmt.Errorf("%w: %v", outbox.ErrPermanent, err)
		var apiErr *telegoapi.Error
		if errors.As(err, &apiErr) && apiErr.ErrorCode == 403 {
			wrapped = errors.Join(wrapped, ErrKicked)
		}
		return wrapped
	}
	if retryAfter > 0 {
		// Sleeping here is deliberate and bounded: Telegram told us exactly
		// how long to wait, and the worker's own backoff is coarser.
		time.Sleep(retryAfter)
	}
	return err
}

// ClassifyError decides whether waiting could ever help, and how long
// Telegram asked us to wait.
func ClassifyError(err error) (permanent bool, retryAfter time.Duration) {
	var apiErr *telegoapi.Error
	if !errors.As(err, &apiErr) {
		// Network-level failures are transient by nature.
		return false, 0
	}

	if apiErr.Parameters != nil && apiErr.Parameters.RetryAfter > 0 {
		return false, time.Duration(apiErr.Parameters.RetryAfter) * time.Second
	}

	switch apiErr.ErrorCode {
	case 403: // kicked, blocked, or no longer a member
		return true, 0
	case 400:
		desc := strings.ToLower(apiErr.Description)
		// "chat not found" and friends never recover; a malformed request
		// will not become well-formed on retry either.
		return strings.Contains(desc, "chat not found") ||
			strings.Contains(desc, "group chat was upgraded") ||
			strings.Contains(desc, "can't parse entities"), 0
	default:
		return false, 0
	}
}

func isTopicMissing(err error) bool {
	var apiErr *telegoapi.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	desc := strings.ToLower(apiErr.Description)
	return strings.Contains(desc, "message thread not found") ||
		strings.Contains(desc, "topic_deleted") ||
		strings.Contains(desc, "topic was deleted")
}

func isCustomEmojiRejected(err error) bool {
	var apiErr *telegoapi.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	desc := strings.ToLower(apiErr.Description)
	return strings.Contains(desc, "custom emoji")
}

// Split breaks a long message on line boundaries. Splitting mid-tag would
// produce invalid HTML, and Telegram rejects the whole message when that
// happens.
func Split(html string, limit int) []string {
	if len([]rune(html)) <= limit {
		return []string{html}
	}

	var (
		parts   []string
		current strings.Builder
	)
	flush := func() {
		if current.Len() > 0 {
			parts = append(parts, strings.TrimRight(current.String(), "\n"))
			current.Reset()
		}
	}

	for _, line := range strings.Split(html, "\n") {
		// A single line longer than the limit is cut on a rune boundary;
		// this only happens with pathological input.
		for len([]rune(line)) > limit {
			runes := []rune(line)
			flush()
			parts = append(parts, string(runes[:limit]))
			line = string(runes[limit:])
		}
		if len([]rune(current.String()))+len([]rune(line))+1 > limit {
			flush()
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	flush()
	return parts
}
