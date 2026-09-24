package tg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
	"github.com/faustyu/gh-notify-go/internal/tg/ui/screens"
)

type HandlerDeps struct {
	Engine     *ui.Engine
	Anchor     *Anchor
	Store      *storage.Store
	Integrator *service.Integrator
	Guard      *Guard
	BotUser    string
	Loc        *i18n.Bundle

	// Broadcaster, when set, is woken the moment a broadcast is confirmed
	// instead of on its next poll.
	Broadcaster *Broadcaster
}

// RegisterHandlers wires every Telegram update this bot reacts to: /start,
// /admin, a callback tap, a reply to the bot's ForceReply prompt, and being
// added to a group.
func RegisterHandlers(bh *th.BotHandler, deps HandlerDeps) {
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return handleStart(ctx, deps, message)
	}, th.CommandEqual("start"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return handleAdmin(ctx, deps, message)
	}, th.CommandEqual("admin"))

	// Groups are watched for one thing only: which forum topics exist. The
	// handler runs before the reply handler because a group message is never
	// an answer to a ForceReply prompt, which only ever happens in DM.
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return recordTopic(ctx, deps, message)
	}, func(_ context.Context, update telego.Update) bool {
		return update.Message != nil &&
			update.Message.Chat.Type != telego.ChatTypePrivate
	})

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

	// /start inside a forum topic is also how an admin reveals that topic to
	// the picker: Telegram delivers commands to the bot whatever its privacy
	// setting, so this works in a group where ordinary messages do not reach
	// it at all.
	if message.Chat.Type != telego.ChatTypePrivate {
		if err := recordTopic(ctx, deps, message); err != nil {
			return err
		}
	}

	_, arg, _ := strings.Cut(message.Text, " ")
	arg = strings.TrimSpace(arg)

	// A referral deep link attributes a brand-new user to the ad that
	// brought them; for everyone else it is a plain /start.
	var (
		userID int64
		lang   = i18n.Normalize(message.From.LanguageCode)
		err    error
	)
	if code, ok := strings.CutPrefix(arg, screens.RefStartPrefix); ok &&
		message.Chat.Type == telego.ChatTypePrivate {
		userID, lang, err = deps.Store.UpsertUserWithRef(ctx, message.From.ID, lang, code)
	} else {
		userID, lang, err = deps.Store.UpsertUser(ctx, message.From.ID, lang)
	}
	if err != nil {
		return err
	}

	// Deep links jump straight to a screen: the group onboarding button
	// carries chat_<id>, GitHub's setup redirect carries installed_<id>.
	screen, params := "home", ui.Params(nil)
	switch {
	case strings.HasPrefix(arg, "chat_"):
		screen, params = "chat_detail", ui.Params{"chat": strings.TrimPrefix(arg, "chat_")}
	case strings.HasPrefix(arg, "installed_"):
		screen, params = "accounts", nil
	}

	view, err := deps.Engine.Open(ctx, userID, message.From.ID, screen, params, lang)
	if err != nil {
		// chat_<id> is user-typed text: any chat id at all can arrive here,
		// including one the sender has nothing to do with.
		status, refused := refusalStatus(err)
		if !refused {
			return err
		}
		view, err = deps.Engine.Open(ctx, userID, message.From.ID, "result",
			ui.Params{"status": status}, lang)
		if err != nil {
			return err
		}
	}
	// /start is the way back from a deleted anchor, so it always posts a new
	// menu rather than editing one the user may no longer see. The command
	// and the older menus stay: every menu in the chat keeps working.
	return deps.Anchor.Reset(ctx, userID, message.From.ID, view)
}

// handleAdmin opens the admin panel for the bot's owners. Anyone else gets
// the ordinary /start, so the command gives nothing away.
func handleAdmin(ctx *th.Context, deps HandlerDeps, message telego.Message) error {
	if message.From == nil || message.Chat.Type != telego.ChatTypePrivate {
		return nil
	}
	if !deps.Guard.IsBotAdmin(message.From.ID) {
		return handleStart(ctx, deps, message)
	}
	userID, lang, err := deps.Store.UpsertUser(ctx, message.From.ID,
		i18n.Normalize(message.From.LanguageCode))
	if err != nil {
		return err
	}
	view, err := deps.Engine.Open(ctx, userID, message.From.ID, "adm_home", nil, lang)
	if err != nil {
		return err
	}
	return deps.Anchor.Reset(ctx, userID, message.From.ID, view)
}

// deny lands a refused user on the shared result screen rather than leaving
// the tap silently unanswered.
func deny(
	ctx *th.Context, deps HandlerDeps, userID, telegramID int64, lang, status string,
) error {
	view, err := deps.Engine.Open(ctx, userID, telegramID, "result",
		ui.Params{"status": status}, lang)
	if err != nil {
		return err
	}
	return deps.Anchor.Show(ctx, userID, telegramID, view)
}

// refusalStatus names the result screen that explains a refusal: being told
// "you are not an administrator" when the real answer is "this repository
// belongs to another admin" would send people looking for the wrong problem.
func refusalStatus(err error) (string, bool) {
	switch {
	case errors.Is(err, service.ErrNotAdmin):
		return "not_admin", true
	case errors.Is(err, service.ErrNotOwner):
		return "not_owner", true
	}
	return "", false
}

func handleCallback(ctx *th.Context, deps HandlerDeps, query telego.CallbackQuery) error {
	// Answering first stops the client's spinner even if the work below is
	// slow or fails.
	defer func() {
		_ = ctx.Bot().AnswerCallbackQuery(ctx,
			&telego.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
	}()

	userID, lang, err := deps.Store.UpsertUser(ctx, query.From.ID,
		i18n.Normalize(query.From.LanguageCode))
	if err != nil {
		return err
	}

	// Every menu /start ever posted stays usable: the one tapped is the one
	// that changes.
	if query.Message != nil && query.Message.GetChat().ID == query.From.ID {
		if err := deps.Anchor.Adopt(ctx, userID, query.Message.GetMessageID()); err != nil {
			return err
		}
	}

	screen, params, err := deps.Engine.Resolve(ctx, userID, query.Data)
	if err != nil {
		if errors.Is(err, ui.ErrActionNotFound) {
			// The button came from a screen older than the action retention
			// window. Send the user home rather than failing silently.
			view, openErr := deps.Engine.Open(ctx, userID, query.From.ID, "home", nil, lang)
			if openErr != nil {
				return openErr
			}
			return deps.Anchor.Show(ctx, userID, query.From.ID, view)
		}
		return err
	}

	// Actions never reach the engine, so they are authorized here; screens are
	// authorized again inside the engine, which costs nothing beyond a cache
	// hit and covers the paths that skip this handler.
	if err := deps.Guard.Authorize(ctx, query.From.ID, screen, params); err != nil {
		if status, refused := refusalStatus(err); refused {
			return deny(ctx, deps, userID, query.From.ID, lang, status)
		}
		return err
	}

	if ui.IsBack(screen) {
		view, err := deps.Engine.Back(ctx, userID, query.From.ID, lang)
		if err != nil {
			if status, refused := refusalStatus(err); refused {
				return deny(ctx, deps, userID, query.From.ID, lang, status)
			}
			return err
		}
		return deps.Anchor.Show(ctx, userID, query.From.ID, view)
	}

	// Actions perform work and route back to a screen; everything else is a
	// screen name and just opens.
	switch screen {
	case "connect":
		return handleConnect(ctx, deps, query, userID, lang, params)
	case "a_mute":
		return applyMute(ctx, deps, userID, query.From.ID, lang, params)
	case "a_topic":
		return applyTopic(ctx, deps, userID, query.From.ID, lang, params)
	case "a_topic_new":
		return createTopic(ctx, deps, userID, query.From.ID, lang, params)
	case "a_ev_toggle":
		return toggleEvent(ctx, deps, userID, query.From.ID, lang, params)
	case "a_ev_preset":
		return applyEventPreset(ctx, deps, userID, query.From.ID, lang, params)
	case "a_user_lang":
		return applyUserLanguage(ctx, deps, userID, query.From.ID, params)
	case "a_filter_add":
		return startInput(ctx, deps, userID, query.From.ID, lang, "filter", params,
			deps.Loc.Localizer(lang).T("prompt.filter", "kind", params["kind"]))
	case "a_filter_del":
		// The filters screen carries no chat param, so the chat the audit
		// entry belongs to is resolved before the row goes away.
		telegramChatID, err := deps.Store.TelegramChatForFilter(ctx, paramInt(params["filter"]))
		if err != nil {
			return err
		}
		if err := deps.Store.DeleteFilter(ctx, paramInt(params["filter"])); err != nil {
			return err
		}
		audit(ctx, deps, userID, telegramChatID, "integration.filter_delete",
			map[string]any{"filter": params["filter"]})
		return reopen(ctx, deps, userID, query.From.ID, lang, "filters", params)
	case "a_refresh":
		return refresh(ctx, deps, userID, query.From.ID, lang, params)
	case "adm_ref_new":
		return startInput(ctx, deps, userID, query.From.ID, lang, "ref_name", nil,
			deps.Loc.Localizer(lang).T("admin.prompt.ref_name"))
	case "adm_ref_del":
		if err := deps.Store.DeleteRefLink(ctx, paramInt(params["ref"])); err != nil {
			return err
		}
		audit(ctx, deps, userID, 0, "admin.ref_delete", map[string]any{"ref": params["ref"]})
		// The deleted link's screen is on top of the stack; underneath is
		// the list it was opened from.
		view, err := deps.Engine.Back(ctx, userID, query.From.ID, lang)
		if err != nil {
			return err
		}
		return deps.Anchor.Show(ctx, userID, query.From.ID, view)
	case "adm_bc_new":
		return startInput(ctx, deps, userID, query.From.ID, lang, "broadcast", nil,
			deps.Loc.Localizer(lang).T("admin.prompt.broadcast"))
	case "adm_bc_send":
		if err := deps.Store.StartBroadcast(ctx, paramInt(params["bc"])); err != nil {
			return err
		}
		audit(ctx, deps, userID, 0, "admin.broadcast_start", map[string]any{"bc": params["bc"]})
		if deps.Broadcaster != nil {
			deps.Broadcaster.Wake()
		}
		return refresh(ctx, deps, userID, query.From.ID, lang,
			ui.Params{"screen": "adm_bc", "bc": params["bc"]})
	case "adm_bc_cancel":
		if err := deps.Store.CancelBroadcast(ctx, paramInt(params["bc"])); err != nil {
			return err
		}
		audit(ctx, deps, userID, 0, "admin.broadcast_cancel", map[string]any{"bc": params["bc"]})
		return refresh(ctx, deps, userID, query.From.ID, lang,
			ui.Params{"screen": "adm_bc", "bc": params["bc"]})
	case "a_int_del":
		if err := deps.Store.DeleteIntegration(ctx, paramInt(params["integration"])); err != nil {
			return err
		}
		audit(ctx, deps, userID, paramInt(params["chat"]), "integration.delete",
			map[string]any{"repo": params["name"]})
		return reopen(ctx, deps, userID, query.From.ID, lang, "chat_detail", params)
	}

	view, err := deps.Engine.Open(ctx, userID, query.From.ID, screen, params, lang)
	if err != nil {
		if status, refused := refusalStatus(err); refused {
			return deny(ctx, deps, userID, query.From.ID, lang, status)
		}
		return err
	}
	return deps.Anchor.Show(ctx, userID, query.From.ID, view)
}

// reopen re-renders a screen after an action changed the data under it.
func reopen(
	ctx *th.Context, deps HandlerDeps, userID, telegramID int64,
	lang, screen string, params ui.Params,
) error {
	view, err := deps.Engine.Open(ctx, userID, telegramID, screen, params, lang)
	if err != nil {
		return err
	}
	return deps.Anchor.Show(ctx, userID, telegramID, view)
}

// refresh re-draws params["screen"] in place, without a new stack frame.
// The screen is authorized inside the engine like any other.
func refresh(
	ctx *th.Context, deps HandlerDeps, userID, telegramID int64,
	lang string, params ui.Params,
) error {
	target := make(ui.Params, len(params))
	for k, v := range params {
		if k != "screen" {
			target[k] = v
		}
	}
	view, err := deps.Engine.Refresh(ctx, userID, telegramID, params["screen"], target, lang)
	if err != nil {
		if status, refused := refusalStatus(err); refused {
			return deny(ctx, deps, userID, telegramID, lang, status)
		}
		return err
	}
	return deps.Anchor.Show(ctx, userID, telegramID, view)
}

func paramInt(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func applyMute(
	ctx *th.Context, deps HandlerDeps, userID, telegramID int64,
	lang string, params ui.Params,
) error {
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
	return reopen(ctx, deps, userID, telegramID, lang, "chat_detail", params)
}

// applyUserLanguage records an explicit interface-language choice and
// re-renders the settings screen in the new language immediately.
func applyUserLanguage(
	ctx *th.Context, deps HandlerDeps, userID, telegramID int64, params ui.Params,
) error {
	lang := i18n.Normalize(params["lang"])
	if err := deps.Store.SetUserLanguage(ctx, userID, lang); err != nil {
		return err
	}
	audit(ctx, deps, userID, 0, "user.language", map[string]any{"lang": lang})
	return reopen(ctx, deps, userID, telegramID, lang, "settings", nil)
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

func toggleEvent(
	ctx *th.Context, deps HandlerDeps, userID, telegramID int64,
	lang string, params ui.Params,
) error {
	enabled := params["to"] == "1"
	if err := deps.Store.SetEventEnabled(ctx,
		paramInt(params["integration"]), params["kind"], enabled); err != nil {
		return err
	}
	audit(ctx, deps, userID, paramInt(params["chat"]), "integration.events",
		map[string]any{"kind": params["kind"], "enabled": enabled})
	return reopen(ctx, deps, userID, telegramID, lang, "events", params)
}

func applyEventPreset(
	ctx *th.Context, deps HandlerDeps, userID, telegramID int64,
	lang string, params ui.Params,
) error {
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
	return reopen(ctx, deps, userID, telegramID, lang, "events", params)
}

// promptParam carries the prompt's message id inside the pending input, so
// the prompt can be cleaned up whether or not the answer quotes it.
const promptParam = "_prompt"

// startInput prompts the user and remembers what their next message feeds;
// handleReplyInput consumes it.
func startInput(
	ctx *th.Context, deps HandlerDeps, userID, telegramID int64,
	lang, action string, params ui.Params, prompt string,
) error {
	sent, err := ctx.Bot().SendMessage(ctx, &telego.SendMessageParams{
		ChatID:      telego.ChatID{ID: telegramID},
		Text:        prompt,
		ReplyMarkup: &telego.ForceReply{ForceReply: true},
	})
	if err != nil {
		return err
	}
	pending := make(ui.Params, len(params)+1)
	for k, v := range params {
		pending[k] = v
	}
	pending[promptParam] = strconv.Itoa(sent.MessageID)
	return deps.Store.SetPendingInput(ctx, userID, action, pending)
}

// handleReplyInput consumes the user's answer to a prompt: applies it,
// deletes both the prompt and the answer to keep the chat clean, and
// refreshes the screen the input belonged to.
//
// The answer does not have to quote the prompt. ForceReply is only a hint:
// desktop clients drop it as soon as the user clicks elsewhere, and the
// answer then arrives as a plain message. While input is pending, the next
// private message is the answer.
func handleReplyInput(ctx *th.Context, deps HandlerDeps, message telego.Message) error {
	if message.Chat.Type != telego.ChatTypePrivate ||
		message.From == nil || strings.HasPrefix(message.Text, "/") {
		return nil
	}

	userID, lang, err := deps.Store.UpsertUser(ctx, message.From.ID,
		i18n.Normalize(message.From.LanguageCode))
	if err != nil {
		return err
	}
	l := deps.Loc.Localizer(lang)

	action, params, err := deps.Store.TakePendingInput(ctx, userID)
	if err != nil {
		return err
	}
	if action == "" {
		return nil
	}
	promptID := int(paramInt(params[promptParam]))
	delete(params, promptParam)

	// The prompt was authorized when it was sent, but it can sit unanswered
	// for as long as the user likes, so the write is authorized again here.
	if err := deps.Guard.Authorize(ctx, message.From.ID, pendingScope(action), params); err != nil {
		status, refused := refusalStatus(err)
		if !refused {
			return err
		}
		dropPrompt(ctx, message, promptID)
		return deny(ctx, deps, userID, message.From.ID, lang, status)
	}

	var (
		screen = "home"
		reply  = strings.TrimSpace(message.Text)
		notice string
	)
	switch action {
	case "broadcast":
		// The admin's message is the broadcast: it is copied to everyone
		// from where it sits, so it must not be deleted like other answers.
		// Only the prompt goes, and the confirmation is posted below the
		// message it is about.
		dropMessage(ctx, message.Chat.ID, promptID)
		id, err := deps.Store.CreateBroadcast(ctx, userID, message.Chat.ID, message.MessageID)
		if err != nil {
			return err
		}
		view, err := deps.Engine.Open(ctx, userID, message.From.ID, "adm_bc",
			ui.Params{"bc": strconv.FormatInt(id, 10)}, lang)
		if err != nil {
			return err
		}
		return deps.Anchor.Reset(ctx, userID, message.From.ID, view)
	case "ref_name":
		screen = "adm_refs"
		if reply == "" || len([]rune(reply)) > 64 {
			notice = l.T("admin.prompt.ref_name_invalid")
			break
		}
		link, err := deps.Store.CreateRefLink(ctx, userID, reply)
		if err != nil {
			return err
		}
		audit(ctx, deps, userID, 0, "admin.ref_create", map[string]any{"ref": link.ID})
		screen, params = "adm_ref", ui.Params{"ref": strconv.FormatInt(link.ID, 10)}
	case "filter":
		screen = "filters"
		if reply == "" || len(reply) > 100 {
			notice = l.T("prompt.filter_invalid")
			break
		}
		if _, err := deps.Store.AddFilter(ctx,
			paramInt(params["integration"]), params["kind"], reply); err != nil {
			return err
		}
		audit(ctx, deps, userID, paramInt(params["chat"]), "integration.filter_add",
			map[string]any{"kind": params["kind"], "pattern": reply})
	}

	dropPrompt(ctx, message, promptID)

	if notice != "" {
		_, _ = ctx.Bot().SendMessage(ctx, &telego.SendMessageParams{
			ChatID: telego.ChatID{ID: message.Chat.ID}, Text: notice,
		})
	}
	return reopen(ctx, deps, userID, message.From.ID, lang, screen, params)
}

// pendingScope names the action a ForceReply answer completes, so the same
// authorization table covers the prompt and the write it produces.
func pendingScope(action string) string {
	switch action {
	case "filter":
		return "a_filter_add"
	case "ref_name":
		return "adm_ref_new"
	case "broadcast":
		return "adm_bc_new"
	}
	return action
}

// dropPrompt removes the bot's prompt and the user's answer once they have
// served their purpose.
func dropPrompt(ctx *th.Context, message telego.Message, promptID int) {
	dropMessage(ctx, message.Chat.ID, promptID)
	dropMessage(ctx, message.Chat.ID, message.MessageID)
}

func dropMessage(ctx *th.Context, chatID int64, messageID int) {
	if messageID == 0 {
		return
	}
	_ = ctx.Bot().DeleteMessage(ctx, &telego.DeleteMessageParams{
		ChatID: telego.ChatID{ID: chatID}, MessageID: messageID,
	})
}

func handleConnect(
	ctx *th.Context, deps HandlerDeps, query telego.CallbackQuery,
	userID int64, lang string, params ui.Params,
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
		ui.Params{"status": status, "name": params["name"]}, lang)
	if openErr != nil {
		return openErr
	}
	return deps.Anchor.Show(ctx, userID, query.From.ID, view)
}

// handleAddedToChat greets a group exactly once, with a single button that
// carries the user into DM. The bot says nothing else in groups. The greeting
// speaks the language of whoever added the bot, which is also the language
// the chat's notifications start in.
func handleAddedToChat(ctx *th.Context, deps HandlerDeps, update telego.ChatMemberUpdated) error {
	if update.Chat.Type == telego.ChatTypePrivate {
		return nil
	}

	status := update.NewChatMember.MemberStatus()
	if status == telego.MemberStatusLeft || status == telego.MemberStatusBanned {
		// Removed from the chat: drop it, so it stops showing in the picker
		// and the chats screen instead of lingering as a dead entry.
		return deps.Store.DeleteChat(ctx, update.Chat.ID)
	}
	if status != telego.MemberStatusMember && status != telego.MemberStatusAdministrator {
		return nil
	}

	lang := i18n.Normalize(update.From.LanguageCode)
	chatID, err := deps.Store.UpsertChat(ctx, update.Chat.ID, update.Chat.Title,
		update.Chat.Type, update.Chat.IsForum)
	if err != nil {
		return err
	}

	// Whoever added the bot becomes a candidate manager of this chat, which
	// is what puts it in their chat picker. Their admin rights are verified
	// again when they actually connect a repository.
	adderID, _, err := deps.Store.UpsertUser(ctx, update.From.ID, lang)
	if err != nil {
		return err
	}
	if err := deps.Store.AddChatManager(ctx, chatID, adderID); err != nil {
		return err
	}
	// Back in the chat: whatever was broken by the removal works again.
	if err := deps.Store.ClearChatIntegrationsBroken(ctx, update.Chat.ID); err != nil {
		return err
	}

	l := deps.Loc.Localizer(lang)
	_, err = ctx.Bot().SendMessage(ctx, &telego.SendMessageParams{
		ChatID:    telego.ChatID{ID: update.Chat.ID},
		Text:      render.Emoji(render.EmojiBot, "🤖") + " <b>GitHub Notify</b>\n\n" + l.T("greeting.body"),
		ParseMode: telego.ModeHTML,
		ReplyMarkup: &telego.InlineKeyboardMarkup{
			InlineKeyboard: [][]telego.InlineKeyboardButton{{{
				Text:              l.T("greeting.button"),
				IconCustomEmojiID: render.EmojiSettings,
				URL: "https://t.me/" + deps.BotUser + "?start=chat_" +
					fmt.Sprint(update.Chat.ID),
			}}},
		},
	})
	return err
}
