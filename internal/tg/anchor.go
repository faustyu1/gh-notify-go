package tg

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegoapi"

	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type AnchorAPI interface {
	SendMessage(ctx context.Context, params *telego.SendMessageParams) (*telego.Message, error)
	EditMessageText(ctx context.Context, params *telego.EditMessageTextParams) (*telego.Message, error)
	DeleteMessage(ctx context.Context, params *telego.DeleteMessageParams) error
}

// Anchor keeps one message per user acting as the whole interface. Screens
// are edited into it instead of being appended to the chat.
type Anchor struct {
	api    AnchorAPI
	engine *ui.Engine
	nav    ui.NavStore
}

func NewAnchor(api AnchorAPI, engine *ui.Engine, nav ui.NavStore) *Anchor {
	return &Anchor{api: api, engine: engine, nav: nav}
}

func (a *Anchor) Show(ctx context.Context, userID, telegramID int64, view ui.View) error {
	markup, err := Keyboard(ctx, a.engine, userID, view)
	if err != nil {
		return err
	}

	messageID, err := a.nav.AnchorMessageID(ctx, userID)
	if err != nil {
		return err
	}

	if messageID != 0 {
		_, err := a.api.EditMessageText(ctx, &telego.EditMessageTextParams{
			ChatID:             telego.ChatID{ID: telegramID},
			MessageID:          messageID,
			Text:               view.Text,
			ParseMode:          telego.ModeHTML,
			ReplyMarkup:        markup,
			LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true},
		})
		if err == nil {
			return nil
		}
		// Tapping the button of the screen you are already on produces an
		// identical edit; that is a no-op, not a failure.
		if isNotModified(err) {
			return nil
		}
		// Any other refusal means this message id is unusable — the user
		// deleted the anchor, it aged out, Telegram worded the reason
		// differently. Whatever the wording, the interface must not go dead:
		// fall through and post a fresh anchor.
		if !isEditRefused(err) {
			return fmt.Errorf("edit anchor: %w", err)
		}
	}

	return a.send(ctx, userID, telegramID, view, markup)
}

// Reset drops the current anchor and posts a fresh one. A user who deletes the
// anchor in a private chat only removes their own copy: the message still
// exists for the bot, so every later edit succeeds against a message nobody can
// see and the interface looks dead. /start therefore never edits — it starts a
// new anchor, deleting the old one so no duplicate is left behind for users who
// still have it.
func (a *Anchor) Reset(ctx context.Context, userID, telegramID int64, view ui.View) error {
	markup, err := Keyboard(ctx, a.engine, userID, view)
	if err != nil {
		return err
	}

	messageID, err := a.nav.AnchorMessageID(ctx, userID)
	if err != nil {
		return err
	}
	if messageID != 0 {
		// Already gone on the user's side, too old to delete, whatever: the
		// replacement matters, the cleanup does not.
		_ = a.api.DeleteMessage(ctx, &telego.DeleteMessageParams{
			ChatID: telego.ChatID{ID: telegramID}, MessageID: messageID,
		})
	}

	return a.send(ctx, userID, telegramID, view, markup)
}

func (a *Anchor) send(
	ctx context.Context, userID, telegramID int64,
	view ui.View, markup *telego.InlineKeyboardMarkup,
) error {
	sent, err := a.api.SendMessage(ctx, &telego.SendMessageParams{
		ChatID:             telego.ChatID{ID: telegramID},
		Text:               view.Text,
		ParseMode:          telego.ModeHTML,
		ReplyMarkup:        markup,
		LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true},
	})
	if err != nil {
		return fmt.Errorf("send anchor: %w", err)
	}
	return a.nav.SetAnchorMessageID(ctx, userID, sent.MessageID)
}

// Keyboard converts a View into Telegram markup, storing each navigation
// target behind a short opaque key.
func Keyboard(
	ctx context.Context, engine *ui.Engine, userID int64, view ui.View,
) (*telego.InlineKeyboardMarkup, error) {
	rows := make([][]telego.InlineKeyboardButton, 0, len(view.Rows))

	for _, row := range view.Rows {
		out := make([]telego.InlineKeyboardButton, 0, len(row))
		for _, button := range row {
			if button.URL != "" {
				out = append(out, telego.InlineKeyboardButton{
					Text: button.Label, URL: button.URL,
				})
				continue
			}
			key, err := engine.ActionKey(ctx, userID, button.Screen, button.Params)
			if err != nil {
				return nil, err
			}
			out = append(out, telego.InlineKeyboardButton{
				Text: button.Label, CallbackData: key,
			})
		}
		if len(out) > 0 {
			rows = append(rows, out)
		}
	}
	return &telego.InlineKeyboardMarkup{InlineKeyboard: rows}, nil
}

func isNotModified(err error) bool {
	return descriptionContains(err, "message is not modified")
}

// isEditRefused reports that Telegram rejected the edit itself, rather than
// the request failing to reach it. Listing the descriptions was too narrow: a
// deleted anchor comes back as "message to edit not found" or as
// "MESSAGE_ID_INVALID" depending on the case, and only the first was covered,
// so deleting the anchor left the user with no interface at all. A transport
// failure carries no API error and still surfaces.
func isEditRefused(err error) bool {
	var apiErr *telegoapi.Error
	return errors.As(err, &apiErr) && apiErr.ErrorCode == http.StatusBadRequest
}

func descriptionContains(err error, needle string) bool {
	var apiErr *telegoapi.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	return strings.Contains(strings.ToLower(apiErr.Description), needle)
}
