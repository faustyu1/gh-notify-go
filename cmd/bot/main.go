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
	"github.com/faustyu/gh-notify-go/internal/ghapp"
	"github.com/faustyu/gh-notify-go/internal/httpapi"
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
	})

	nav := ui.NewPostgresNav(store.Pool())
	engine := ui.NewEngine(nav)
	engine.Register(
		screens.NewHome(store),
		screens.NewInstall(cfg.GitHub.Slug, cfg.HTTP.PublicURL),
		screens.NewAccounts(store),
		screens.NewRepos(store, github, 10),
		screens.NewRepoDetail(store),
		screens.NewChatPicker(store),
		screens.NewAddToChat(cfg.Bot.Username),
		screens.NewResult(),
		// Implemented by the follow-up plans; registered now so no button
		// in the shipped interface leads nowhere.
		screens.NewPlaceholder("chats", "Чаты"),
		screens.NewPlaceholder("chat_detail", "Настройки чата"),
		screens.NewPlaceholder("status", "Статус"),
		screens.NewPlaceholder("settings", "Настройки"),
	)

	integrator := service.NewIntegrator(store, tg.NewAdminChecker(bot, time.Minute))
	ingest := service.NewIngest(store, queue)
	installations := service.NewInstallations(store, github)

	mux := http.NewServeMux()
	mux.Handle("/gh/webhook", httpapi.NewWebhookHandler(cfg.GitHub.WebhookSecret, ingest))
	mux.Handle("/github/setup", httpapi.NewSetupHandler(installations, cfg.Bot.Username))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	updates, err := bot.UpdatesViaLongPolling(ctx, nil)
	if err != nil {
		return err
	}
	handler, err := th.NewBotHandler(bot, updates)
	if err != nil {
		return err
	}
	tg.RegisterHandlers(handler, tg.HandlerDeps{
		Engine:     engine,
		Anchor:     tg.NewAnchor(bot, engine, nav),
		Store:      store,
		Integrator: integrator,
		BotUser:    cfg.Bot.Username,
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
			outbox.NewWorker(store.Pool(), sender, time.Now).Run(ctx, time.Second)
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
