package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/miksea/bot_discord_go/internal/bot"
	"github.com/miksea/bot_discord_go/internal/config"
	"github.com/miksea/bot_discord_go/pkg/logger"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	env := os.Getenv("APP_ENV")
	log := logger.New(env)

	cfg, err := config.Load()
	if err != nil {
		log.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	b, err := bot.New(cfg, log)
	if err != nil {
		log.Error("failed to initialize bot", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := b.Start(ctx); err != nil {
		log.Error("bot exited with error", "error", err)
		os.Exit(1)
	}

	log.Log(context.Background(), slog.LevelInfo, "bye!")
}
