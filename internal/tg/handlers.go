package tg

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
)

type HandlerDeps struct {
	Engine     *ui.Engine
	Anchor     *Anchor
	Store      *storage.Store
	Integrator *service.Integrator
	BotUser    string
}

// RegisterHandlers wires every Telegram update this bot reacts to: /start, a
// callback tap, a reply to the bot's ForceReply prompt, and being added to a
// group.
func RegisterHandlers(bh *th.BotHandler, deps HandlerDeps) {
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return handleStart(ctx, deps, message)
	}, th.CommandEqual("start"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return handleReplyInput(ctx, deps, message)
	})

	bh.HandleCallbackQuery(func(ctx *th.Context, query telego.CallbackQuery) error {
		return handleCallback(ctx, deps, query)
	}, th.AnyCallbackQueryWithMessage())

	bh.HandleMyChatMember(func(ctx *th.Context, update telego.ChatMemberUpdated) error {
		return handleAddedToChat(ctx, deps, update)
	})
}

func handleStart(ctx *th.Context, deps HandlerDeps, message telego.Message) error {
	if message.From == nil {
		return nil
	}
	userID, err := deps.Store.UpsertUser(ctx, message.From.ID)
	if err != nil {
		return err
	}

	// Deep links jump straight to a screen: the group onboarding button
	// carries chat_<id>, GitHub's setup redirect carries installed_<id>.
	screen, params := "home", ui.Params(nil)
	if _, arg, found := strings.Cut(message.Text, " "); found {
		switch {
		case strings.HasPrefix(arg, "chat_"):
			screen, params = "chat_detail", ui.Params{"chat": strings.TrimPrefix(arg, "chat_")}
		case strings.HasPrefix(arg, "installed_"):
			screen, params = "accounts", nil
		}
	}

	view, err := deps.Engine.Open(ctx, userID, message.From.ID, screen, params)
	if err != nil {
		return err
	}
	return deps.Anchor.Show(ctx, userID, message.From.ID, view)
}

func handleCallback(ctx *th.Context, deps HandlerDeps, query telego.CallbackQuery) error {
	// Answering first stops the client's spinner even if the work below is
	// slow or fails.
	defer func() {
		_ = ctx.Bot().AnswerCallbackQuery(ctx,
			&telego.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
	}()

	userID, err := deps.Store.UpsertUser(ctx, query.From.ID)
	if err != nil {
		return err
	}

	screen, params, err := deps.Engine.Resolve(ctx, userID, query.Data)
	if err != nil {
		if errors.Is(err, ui.ErrActionNotFound) {
			// The button came from a screen older than the action retention
			// window. Send the user home rather than failing silently.
			view, openErr := deps.Engine.Open(ctx, userID, query.From.ID, "home", nil)
			if openErr != nil {
				return openErr
			}
			return deps.Anchor.Show(ctx, userID, query.From.ID, view)
		}
		return err
	}

	if ui.IsBack(screen) {
		view, err := deps.Engine.Back(ctx, userID, query.From.ID)
		if err != nil {
			return err
		}
		return deps.Anchor.Show(ctx, userID, query.From.ID, view)
	}

	// Actions perform work and route back to a screen; everything else is a
	// screen name and just opens.
	switch screen {
	case "connect":
		return handleConnect(ctx, deps, query, userID, params)
	case "a_mute":
		return applyMute(ctx, deps, userID, query.From.ID, params)
	case "a_topic":
		return startInput(ctx, deps, userID, query.From.ID, "topic", params,
			"Пришли id топика форума (число). 0 — присылать в общий чат.")
	case "a_ev_toggle":
		return toggleEvent(ctx, deps, userID, query.From.ID, params)
	case "a_ev_preset":
		return applyEventPreset(ctx, deps, userID, query.From.ID, params)
	case "a_filter_add":
		return startInput(ctx, deps, userID, query.From.ID, "filter", params,
			"Пришли шаблон для фильтра («"+params["kind"]+"»). Можно использовать *.")
	case "a_filter_del":
		if err := deps.Store.DeleteFilter(ctx, paramInt(params["filter"])); err != nil {
			return err
		}
		audit(ctx, deps, userID, paramInt(params["chat"]), "integration.filter_delete",
			map[string]any{"filter": params["filter"]})
		return reopen(ctx, deps, userID, query.From.ID, "filters", params)
	case "a_int_del":
		if err := deps.Store.DeleteIntegration(ctx, paramInt(params["integration"])); err != nil {
			return err
		}
		audit(ctx, deps, userID, paramInt(params["chat"]), "integration.delete",
			map[string]any{"repo": params["name"]})
		return reopen(ctx, deps, userID, query.From.ID, "chat_detail", params)
	}

	view, err := deps.Engine.Open(ctx, userID, query.From.ID, screen, params)
	if err != nil {
		return err
	}
	return deps.Anchor.Show(ctx, userID, query.From.ID, view)
}

// reopen re-renders a screen after an action changed the data under it.
func reopen(ctx *th.Context, deps HandlerDeps, userID, telegramID int64, screen string, params ui.Params) error {
	view, err := deps.Engine.Open(ctx, userID, telegramID, screen, params)
	if err != nil {
		return err
	}
	return deps.Anchor.Show(ctx, userID, telegramID, view)
}

func paramInt(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func applyMute(ctx *th.Context, deps HandlerDeps, userID, telegramID int64, params ui.Params) error {
	telegramChatID := paramInt(params["chat"])
	hours := paramInt(params["hours"])

	var until *time.Time
	if hours > 0 {
		t := time.Now().Add(time.Duration(hours) * time.Hour)
		until = &t
	}
	if err := deps.Store.SetChatMute(ctx, telegramChatID, until); err != nil {
		return err
	}
	audit(ctx, deps, userID, telegramChatID, "chat.mute", map[string]any{"hours": hours})
	return reopen(ctx, deps, userID, telegramID, "chat_detail", params)
}

// audit best-effort records an admin action; a logging failure must not
// block the action itself.
func audit(ctx *th.Context, deps HandlerDeps, userID, telegramChatID int64, action string, meta map[string]any) {
	var chatID *int64
	if chat, err := deps.Store.ChatByTelegramID(ctx, telegramChatID); err == nil {
		chatID = &chat.ID
	}
	if err := deps.Store.WriteAudit(ctx, userID, chatID, action, meta); err != nil {
		slog.Warn("audit", "action", action, "error", err)
	}
}

func toggleEvent(ctx *th.Context, deps HandlerDeps, userID, telegramID int64, params ui.Params) error {
	enabled := params["to"] == "1"
	if err := deps.Store.SetEventEnabled(ctx,
		paramInt(params["integration"]), params["kind"], enabled); err != nil {
		return err
	}
	audit(ctx, deps, userID, paramInt(params["chat"]), "integration.events",
		map[string]any{"kind": params["kind"], "enabled": enabled})
	return reopen(ctx, deps, userID, telegramID, "events", params)
}

func applyEventPreset(ctx *th.Context, deps HandlerDeps, userID, telegramID int64, params ui.Params) error {
	integrationID := paramInt(params["integration"])
	// A preset writes an explicit setting for every kind, so it overrides
	// both previous toggles and the "missing row means on" default.
	for _, kind := range events.Kinds() {
		if err := deps.Store.SetEventEnabled(ctx, integrationID,
			string(kind), events.PresetEnabled(params["preset"], kind)); err != nil {
			return err
		}
	}
	audit(ctx, deps, userID, paramInt(params["chat"]), "integration.events_preset",
		map[string]any{"preset": params["preset"]})
	return reopen(ctx, deps, userID, telegramID, "events", params)
}

// startInput remembers what the next reply feeds and prompts the user with
// ForceReply; handleReplyInput consumes it.
func startInput(
	ctx *th.Context, deps HandlerDeps, userID, telegramID int64,
	action string, params ui.Params, prompt string,
) error {
	if err := deps.Store.SetPendingInput(ctx, userID, action, params); err != nil {
		return err
	}
	_, err := ctx.Bot().SendMessage(ctx, &telego.SendMessageParams{
		ChatID:      telego.ChatID{ID: telegramID},
		Text:        prompt,
		ReplyMarkup: &telego.ForceReply{ForceReply: true},
	})
	return err
}

// handleReplyInput consumes the user's answer to a ForceReply prompt: applies
// it, deletes both the prompt and the reply to keep the chat clean, and
// refreshes the screen the input belonged to.
func handleReplyInput(ctx *th.Context, deps HandlerDeps, message telego.Message) error {
	// Only a direct reply from a private chat to our own prompt qualifies.
	if message.Chat.Type != telego.ChatTypePrivate ||
		message.From == nil || strings.HasPrefix(message.Text, "/") {
		return nil
	}
	if message.ReplyToMessage == nil || message.ReplyToMessage.From == nil ||
		!message.ReplyToMessage.From.IsBot {
		return nil
	}

	userID, err := deps.Store.UpsertUser(ctx, message.From.ID)
	if err != nil {
		return err
	}

	action, params, err := deps.Store.TakePendingInput(ctx, userID)
	if err != nil {
		return err
	}
	if action == "" {
		return nil
	}

	var (
		screen = "home"
		reply  = strings.TrimSpace(message.Text)
		notice string
	)
	switch action {
	case "topic":
		screen = "chat_detail"
		topicID, err := strconv.ParseInt(reply, 10, 64)
		if err != nil || topicID < 0 {
			notice = "⚠️ Это не число — топик не изменён."
			break
		}
		var topic *int64
		if topicID > 0 {
			topic = &topicID
		}
		if err := deps.Store.SetChatTopic(ctx, paramInt(params["chat"]), topic); err != nil {
			return err
		}
		audit(ctx, deps, userID, paramInt(params["chat"]), "chat.topic",
			map[string]any{"topic": topicID})
	case "filter":
		screen = "filters"
		if reply == "" || len(reply) > 100 {
			notice = "⚠️ Шаблон пустой или слишком длинный."
			break
		}
		if _, err := deps.Store.AddFilter(ctx,
			paramInt(params["integration"]), params["kind"], reply); err != nil {
			return err
		}
		audit(ctx, deps, userID, paramInt(params["chat"]), "integration.filter_add",
			map[string]any{"kind": params["kind"], "pattern": reply})
	}

	// The prompt and the answer have served their purpose; both go away.
	_ = ctx.Bot().DeleteMessage(ctx, &telego.DeleteMessageParams{
		ChatID: telego.ChatID{ID: message.Chat.ID}, MessageID: message.ReplyToMessage.MessageID,
	})
	_ = ctx.Bot().DeleteMessage(ctx, &telego.DeleteMessageParams{
		ChatID: telego.ChatID{ID: message.Chat.ID}, MessageID: message.MessageID,
	})

	if notice != "" {
		_, _ = ctx.Bot().SendMessage(ctx, &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: message.Chat.ID}, Text: notice,
		})
	}
	return reopen(ctx, deps, userID, message.From.ID, screen, params)
}

func handleConnect(
	ctx *th.Context, deps HandlerDeps, query telego.CallbackQuery,
	userID int64, params ui.Params,
) error {
	installationID, _ := strconv.ParseInt(params["installation"], 10, 64)
	chatID, _ := strconv.ParseInt(params["chat"], 10, 64)
	repoID, _ := strconv.ParseInt(params["repo"], 10, 64)

	var telegramChatID int64
	if err := deps.Store.Pool().QueryRow(ctx,
		`SELECT telegram_chat_id FROM chats WHERE id = $1`, chatID).
		Scan(&telegramChatID); err != nil {
		return err
	}

	status := "ok"
	err := deps.Integrator.Connect(ctx, service.ConnectRequest{
		UserID: userID, TelegramUserID: query.From.ID,
		InstallationID: installationID,
		ChatID:         chatID, TelegramChatID: telegramChatID,
		RepoGitHubID: repoID, RepoFullName: params["name"],
	})
	switch {
	case errors.Is(err, service.ErrNotAdmin):
		status = "not_admin"
	case errors.Is(err, service.ErrAlreadyConnected):
		status = "duplicate"
	case err != nil:
		slog.Error("connect failed", "error", err)
		status = "error"
	}

	view, openErr := deps.Engine.Open(ctx, userID, query.From.ID, "result",
		ui.Params{"status": status, "name": params["name"]})
	if openErr != nil {
		return openErr
	}
	return deps.Anchor.Show(ctx, userID, query.From.ID, view)
}

// handleAddedToChat greets a group exactly once, with a single button that
// carries the user into DM. The bot says nothing else in groups.
func handleAddedToChat(ctx *th.Context, deps HandlerDeps, update telego.ChatMemberUpdated) error {
	status := update.NewChatMember.MemberStatus()
	if status != telego.MemberStatusMember && status != telego.MemberStatusAdministrator {
		return nil
	}
	if update.Chat.Type == telego.ChatTypePrivate {
		return nil
	}

	chatID, err := deps.Store.UpsertChat(ctx, update.Chat.ID, update.Chat.Title, update.Chat.Type)
	if err != nil {
		return err
	}

	// Whoever added the bot becomes a candidate manager of this chat, which
	// is what puts it in their chat picker. Their admin rights are verified
	// again when they actually connect a repository.
	adderID, err := deps.Store.UpsertUser(ctx, update.From.ID)
	if err != nil {
		return err
	}
	if err := deps.Store.AddChatManager(ctx, chatID, adderID); err != nil {
		return err
	}

	_, err = ctx.Bot().SendMessage(ctx, &telego.SendMessageParams{
		ChatID:    telego.ChatID{ID: update.Chat.ID},
		Text:      "🤖 <b>GitHub Notify</b>\n\nНастройка — в личных сообщениях.",
		ParseMode: telego.ModeHTML,
		ReplyMarkup: &telego.InlineKeyboardMarkup{
			InlineKeyboard: [][]telego.InlineKeyboardButton{{{
				Text: "⚙️ Настроить",
				URL: "https://t.me/" + deps.BotUser + "?start=chat_" +
					fmt.Sprint(update.Chat.ID),
			}}},
		},
	})
	return err
}
