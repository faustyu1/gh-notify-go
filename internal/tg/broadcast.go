package tg

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegoapi"

	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/storage"
)

type BroadcastAPI interface {
	CopyMessage(ctx context.Context, params *telego.CopyMessageParams) (*telego.MessageID, error)
	SendMessage(ctx context.Context, params *telego.SendMessageParams) (*telego.Message, error)
}

type BroadcastStore interface {
	NextRunningBroadcast(ctx context.Context) (storage.Broadcast, bool, error)
	BroadcastTargets(ctx context.Context, afterUserID int64, limit int) ([]storage.BroadcastTarget, error)
	AdvanceBroadcast(ctx context.Context, id, lastUserID int64, sent, failed int) (bool, error)
	FinishBroadcast(ctx context.Context, id int64) (storage.Broadcast, bool, error)
	MarkUserBlocked(ctx context.Context, userID int64) error
}

// Broadcaster copies a confirmed broadcast into every reachable DM. Progress
// is committed after each batch, so a restart resumes rather than repeats,
// and a cancel from the admin screen takes effect within one batch.
type Broadcaster struct {
	api   BroadcastAPI
	store BroadcastStore
	loc   *i18n.Bundle
	wake  chan struct{}

	// batch is how many users are messaged between progress commits;
	// interval spaces the messages under Telegram's ~30/s global limit.
	batch    int
	interval time.Duration
}

func NewBroadcaster(api BroadcastAPI, store BroadcastStore, loc *i18n.Bundle) *Broadcaster {
	return &Broadcaster{
		api: api, store: store, loc: loc,
		wake:     make(chan struct{}, 1),
		batch:    25,
		interval: 40 * time.Millisecond,
	}
}

// Wake makes Run look for work now instead of on its next poll.
func (b *Broadcaster) Wake() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// Run processes running broadcasts until ctx ends, polling every idle
// period for one started by another process.
func (b *Broadcaster) Run(ctx context.Context, idle time.Duration) {
	for {
		worked, err := b.step(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("broadcast step", "error", err)
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-b.wake:
		case <-time.After(idle):
		}
	}
}

// step sends one batch of the oldest running broadcast. It reports whether
// there was anything to do.
func (b *Broadcaster) step(ctx context.Context) (bool, error) {
	bc, ok, err := b.store.NextRunningBroadcast(ctx)
	if err != nil || !ok {
		return false, err
	}

	targets, err := b.store.BroadcastTargets(ctx, bc.LastUserID, b.batch)
	if err != nil {
		return false, err
	}
	if len(targets) == 0 {
		return true, b.finish(ctx, bc.ID)
	}

	var sent, failed int
	for _, target := range targets {
		if ctx.Err() != nil {
			// Nothing from this batch is committed; the ones already sent
			// are repeated after a restart, which beats losing the rest.
			return false, ctx.Err()
		}
		switch err := b.copy(ctx, bc, target.TelegramID); {
		case err == nil:
			sent++
		case isBlocked(err):
			failed++
			if markErr := b.store.MarkUserBlocked(ctx, target.UserID); markErr != nil {
				slog.Warn("mark user blocked", "error", markErr)
			}
		default:
			failed++
			slog.Warn("broadcast copy failed", "broadcast", bc.ID, "error", err)
		}
		time.Sleep(b.interval)
	}

	last := targets[len(targets)-1].UserID
	if _, err := b.store.AdvanceBroadcast(ctx, bc.ID, last, sent, failed); err != nil {
		return false, err
	}
	return true, nil
}

// copy sends one copy, waiting out a flood limit once if Telegram asks.
func (b *Broadcaster) copy(ctx context.Context, bc storage.Broadcast, telegramID int64) error {
	params := &telego.CopyMessageParams{
		ChatID:     telego.ChatID{ID: telegramID},
		FromChatID: telego.ChatID{ID: bc.SourceChatID},
		MessageID:  bc.SourceMessageID,
	}
	_, err := b.api.CopyMessage(ctx, params)
	if _, retryAfter := ClassifyError(err); err != nil && retryAfter > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryAfter):
		}
		_, err = b.api.CopyMessage(ctx, params)
	}
	return err
}

func (b *Broadcaster) finish(ctx context.Context, id int64) error {
	bc, finished, err := b.store.FinishBroadcast(ctx, id)
	if err != nil || !finished || bc.AdminTelegramID == 0 {
		return err
	}
	l := b.loc.Localizer(bc.AdminLang)
	_, err = b.api.SendMessage(ctx, &telego.SendMessageParams{
		ChatID:    telego.ChatID{ID: bc.AdminTelegramID},
		ParseMode: telego.ModeHTML,
		Text: "✅ " + l.T("admin.broadcast.done_notice", "id", bc.ID,
			"sent", bc.Sent, "total", bc.Total),
	})
	if err != nil {
		slog.Warn("broadcast done notice", "error", err)
	}
	return nil
}

// isBlocked reports a recipient who blocked the bot or deleted their account.
func isBlocked(err error) bool {
	var apiErr *telegoapi.Error
	return errors.As(err, &apiErr) && apiErr.ErrorCode == 403
}
