package tg

import (
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

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

// RegisterHandlers wires every Telegram update this bot reacts to. There are
// exactly three: /start, a callback tap, and being added to a group.
func RegisterHandlers(bh *th.BotHandler, deps HandlerDeps) {
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return handleStart(ctx, deps, message)
	}, th.CommandEqual("start"))

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

	// A deep link of the form /start chat_<id> jumps straight to that chat's
	// screen, which is how the group onboarding button gets the user here.
	screen, params := "home", ui.Params(nil)
	if _, arg, found := strings.Cut(message.Text, " "); found {
		if chatArg, ok := strings.CutPrefix(strings.TrimSpace(arg), "chat_"); ok {
			screen, params = "chat_detail", ui.Params{"chat": chatArg}
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

	// "connect" is an action, not a screen: it performs work and then routes
	// to the result screen carrying the outcome.
	if screen == "connect" {
		return handleConnect(ctx, deps, query, userID, params)
	}

	view, err := deps.Engine.Open(ctx, userID, query.From.ID, screen, params)
	if err != nil {
		return err
	}
	return deps.Anchor.Show(ctx, userID, query.From.ID, view)
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
					strconv.FormatInt(chatID, 10),
			}}},
		},
	})
	return err
}
