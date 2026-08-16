package tg

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegoapi"

	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type AnchorAPI interface {
	SendMessage(ctx context.Context, params *telego.SendMessageParams) (*telego.Message, error)
	EditMessageText(ctx context.Context, params *telego.EditMessageTextParams) (*telego.Message, error)
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
		if !isMessageGone(err) {
			return fmt.Errorf("edit anchor: %w", err)
		}
	}

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

func isMessageGone(err error) bool {
	return descriptionContains(err, "message to edit not found") ||
		descriptionContains(err, "message can't be edited")
}

func descriptionContains(err error, needle string) bool {
	var apiErr *telegoapi.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	return strings.Contains(strings.ToLower(apiErr.Description), needle)
}
