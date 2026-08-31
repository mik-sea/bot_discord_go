package bot

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/miksea/bot_discord_go/internal/command"
	"github.com/miksea/bot_discord_go/internal/config"
	"github.com/miksea/bot_discord_go/internal/handler"
	"github.com/miksea/bot_discord_go/internal/mailer"
	"github.com/miksea/bot_discord_go/internal/notifier"
	"github.com/miksea/bot_discord_go/internal/planapi"
	"github.com/miksea/bot_discord_go/internal/queue"
	"github.com/miksea/bot_discord_go/internal/store"
	"github.com/miksea/bot_discord_go/internal/watcher"
)

const (
	queueCapacity       = 500
	workerCount         = 3
	watcherPollInterval = 5 * time.Second
	readyTimeout        = 15 * time.Second
)

// Bot is the top-level application struct that wires all components together.
type Bot struct {
	cfg             *config.Config
	logger          *slog.Logger
	session         *discordgo.Session
	queue           *queue.Queue
	server          *http.Server
	watcher         *watcher.FileWatcher
	commandRegistry *command.Registry
	inviteStore     *store.InviteStore
}

// New creates and connects all components. It does NOT start any goroutines.
func New(cfg *config.Config, logger *slog.Logger) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.Discord.Token)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}

	// IntentsGuilds wajib agar Discord mengirim event guild ke bot.
	session.Identify.Intents = discordgo.IntentsGuilds

	inviteStore, err := store.Open(cfg.Database.Path)
	if err != nil {
		return nil, fmt.Errorf("open invite store: %w", err)
	}

	n := notifier.New(session, cfg, inviteStore, logger)
	q := queue.New(queueCapacity, n.Notify, logger)

	webhookHandler := handler.NewWebhookHandler(q, cfg.Server.WebhookSecret, logger)

	mux := http.NewServeMux()
	mux.Handle("POST /webhook", webhookHandler)
	mux.HandleFunc("GET /health", healthHandler)

	srv := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	fw := watcher.New(cfg.Watcher.Dir, q, logger)

	planClient := planapi.New(cfg.PlanAPI.BaseURL, cfg.PlanAPI.APIKey)
	mailClient := mailer.New(cfg.SMTP)
	cmdRegistry := command.NewRegistry(session, cfg, logger, inviteStore, planClient, mailClient)

	return &Bot{
		cfg:             cfg,
		logger:          logger,
		session:         session,
		queue:           q,
		server:          srv,
		watcher:         fw,
		commandRegistry: cmdRegistry,
		inviteStore:     inviteStore,
	}, nil
}

// Start opens the Discord session and begins all background workers.
// Slash command registration terjadi di dalam handler READY — satu-satunya
// titik di mana session.State.User dijamin sudah terisi oleh Discord.
func (b *Bot) Start(ctx context.Context) error {
	// readyCh menerima nil (sukses) atau error dari handler READY.
	readyCh := make(chan error, 1)

	b.session.AddHandler(func(s *discordgo.Session, _ *discordgo.Ready) {
		b.logger.Info("discord READY",
			"username", s.State.User.Username,
			"id", s.State.User.ID,
		)
		readyCh <- b.commandRegistry.Register(b.cfg.Discord.GuildID)
	})

	if err := b.session.Open(); err != nil {
		return fmt.Errorf("open discord session: %w", err)
	}
	b.logger.Info("discord session opening — menunggu event READY...")

	// Tunggu konfirmasi READY atau timeout.
	select {
	case err := <-readyCh:
		if err != nil {
			return fmt.Errorf("register slash commands: %w", err)
		}
	case <-time.After(readyTimeout):
		return fmt.Errorf("timeout %s menunggu Discord READY — periksa token dan koneksi internet", readyTimeout)
	case <-ctx.Done():
		return nil
	}

	b.queue.Start(ctx, workerCount)

	go func() {
		b.logger.Info("HTTP server starting", "addr", b.server.Addr)
		if err := b.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			b.logger.Error("HTTP server error", "error", err)
		}
	}()

	go func() {
		if err := b.watcher.Start(ctx, watcherPollInterval); err != nil {
			b.logger.Error("file watcher error", "error", err)
		}
	}()

	b.logger.Info("bot started successfully")
	<-ctx.Done()

	return b.shutdown()
}

// shutdown performs graceful cleanup in the correct order:
// HTTP server → queue drain → deregister commands → Discord session.
func (b *Bot) shutdown() error {
	b.logger.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := b.server.Shutdown(shutdownCtx); err != nil {
		b.logger.Error("HTTP server shutdown error", "error", err)
	}

	b.queue.Stop()

	// Deregister commands sebelum menutup session agar Discord API call berhasil.
	b.commandRegistry.Deregister(b.cfg.Discord.GuildID)

	if err := b.session.Close(); err != nil {
		b.logger.Error("discord session close error", "error", err)
	}

	if err := b.inviteStore.Close(); err != nil {
		b.logger.Error("invite store close error", "error", err)
	}

	b.logger.Info("shutdown complete")
	return nil
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status":"ok"}`)
}
