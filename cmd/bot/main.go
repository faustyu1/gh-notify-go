// Command bot runs the Telegram bot, the GitHub webhook server, and the
// outbox workers in one process.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"

	"github.com/faustyu/gh-notify-go/internal/config"
	_ "github.com/faustyu/gh-notify-go/internal/events"
	"github.com/faustyu/gh-notify-go/internal/events/render"
	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/httpapi"
	"github.com/faustyu/gh-notify-go/internal/i18n"
	"github.com/faustyu/gh-notify-go/internal/outbox"
	"github.com/faustyu/gh-notify-go/internal/secret"
	"github.com/faustyu/gh-notify-go/internal/service"
	"github.com/faustyu/gh-notify-go/internal/storage"
	"github.com/faustyu/gh-notify-go/internal/storage/migrations"
	"github.com/faustyu/gh-notify-go/internal/tg"
	"github.com/faustyu/gh-notify-go/internal/tg/ui"
	"github.com/faustyu/gh-notify-go/internal/tg/ui/screens"
)

func main() {
	if err := config.LoadDotEnv(".env"); err != nil {
		slog.Warn("read .env", "error", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	box, err := secret.NewBox(os.Getenv("SECRET_KEY"))
	if err != nil {
		return err
	}

	// Every user-visible string lives here; a failure to load the embedded
	// locales is a build defect, not a runtime condition.
	loc := i18n.MustNewBundle()

	if err := migrations.Up(ctx, cfg.Database.URL); err != nil {
		return err
	}

	store, err := storage.New(ctx, cfg.Database.URL, box)
	if err != nil {
		return err
	}
	defer store.Close()

	key, err := ghapp.LoadPrivateKey(cfg.GitHub.PrivateKeyPath)
	if err != nil {
		return err
	}
	tokens := ghapp.NewTokenSource(cfg.GitHub.AppID, key,
		&http.Client{Timeout: 30 * time.Second}, store.TokenCache(), time.Now)
	github := ghapp.NewClient(tokens, &http.Client{Timeout: 30 * time.Second})

	bot, err := telego.NewBot(cfg.Bot.Token)
	if err != nil {
		return err
	}

	queue := outbox.NewQueue(store.Pool(), time.Now)
	sender := tg.NewSender(bot, func(ctx context.Context, chatID int64) error {
		return store.ClearTopic(ctx, chatID)
	}, loc)

	// A row that will never be retried must be heard about: the owner gets a
	// DM, and a kicked bot marks the integration broken.
	onPermanent := func(ctx context.Context, job outbox.Job, err error) {
		owner, repo, ownerLang, lookupErr := store.IntegrationOwner(ctx, job.IntegrationID)
		if lookupErr != nil {
			slog.Error("terminal failure without owner", "job", job.ID, "error", err)
			return
		}
		if errors.Is(err, tg.ErrKicked) {
			if brokenErr := store.MarkIntegrationBroken(ctx, job.IntegrationID, err.Error()); brokenErr != nil {
				slog.Error("mark integration broken", "error", brokenErr)
			}
		}
		l := loc.Localizer(ownerLang)
		_, _ = bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID:    telego.ChatID{ID: owner},
			ParseMode: telego.ModeHTML,
			Text: "⚠️ <b>" + l.T("delivery.failed_title") + "</b>\n\n" +
				l.T("delivery.failed_line", "repo", render.Escape(repo),
					"chat", job.TelegramChatID) +
				"\n\n<code>" + render.Escape(err.Error()) + "</code>",
		})
	}

	// One admin checker for the whole bot: the connect flow and the screen
	// guard share its cache, so a burst of taps stays one Telegram call.
	admins := tg.NewAdminChecker(bot, time.Minute)
	guard := tg.NewGuard(admins, store)

	nav := ui.NewPostgresNav(store.Pool())
	engine := ui.NewEngine(nav, loc).WithGuard(guard.Screen())
	engine.Register(
		screens.NewHome(store, loc),
		screens.NewInstall(cfg.GitHub.Slug, cfg.HTTP.PublicURL, store, loc),
		screens.NewAccounts(store, loc),
		screens.NewRepos(store, github, 10, loc),
		screens.NewRepoDetail(store, loc),
		screens.NewChatPicker(store, loc),
		screens.NewAddToChat(cfg.Bot.Username, loc),
		screens.NewResult(loc),
		screens.NewChats(store, loc),
		screens.NewChatDetail(store, loc),
		screens.NewIntegrationDetail(loc),
		screens.NewEvents(store, loc),
		screens.NewFilters(store, loc),
		screens.NewHealth(store, loc),
		screens.NewStatus(store, loc),
		screens.NewSettings(cfg.Limits.ChatPerMinute, loc),
	)

	integrator := service.NewIntegrator(store, admins)
	ingest := service.NewIngest(store, queue)
	installations := service.NewInstallations(store, github)

	mux := http.NewServeMux()
	mux.Handle("/gh/webhook", httpapi.NewWebhookHandler(cfg.GitHub.WebhookSecret, ingest))
	mux.Handle("/github/setup",
		httpapi.NewSetupHandler(installations, store, cfg.Bot.Username))
	// Liveness that means something: a process that cannot reach Postgres
	// delivers nothing, so it must not report itself healthy.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := store.Pool().Ping(pingCtx); err != nil {
			slog.Warn("health check failed", "error", err)
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	updates, err := bot.UpdatesViaLongPolling(ctx, nil)
	if err != nil {
		return err
	}
	handler, err := th.NewBotHandler(bot, updates)
	if err != nil {
		return err
	}
	// Without this a panic on one update takes the whole process down, which
	// turns any single malformed update into an outage.
	handler.Use(th.PanicRecoveryHandler(func(recovered any) error {
		slog.Error("recovered from panic in update handler", "panic", recovered)
		return nil
	}))
	tg.RegisterHandlers(handler, tg.HandlerDeps{
		Engine:     engine,
		Anchor:     tg.NewAnchor(bot, engine, nav),
		Store:      store,
		Integrator: integrator,
		Guard:      guard,
		BotUser:    cfg.Bot.Username,
		Loc:        loc,
	})

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		slog.Info("http listening", "addr", cfg.HTTP.Addr)
		if err := server.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server stopped", "error", err)
		}
	}()

	for i := range cfg.Limits.Workers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			slog.Info("outbox worker started", "n", n)
			outbox.NewWorker(store.Pool(), sender, time.Now).
				WithChatPerMinute(cfg.Limits.ChatPerMinute).
				WithOnPermanent(onPermanent).
				Run(ctx, time.Second)
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := handler.Start(); err != nil {
			slog.Error("bot handler stopped", "error", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	_ = handler.StopWithContext(shutdownCtx)

	wg.Wait()
	return nil
}
