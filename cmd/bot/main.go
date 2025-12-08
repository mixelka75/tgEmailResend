package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lmittmann/tint"
	"github.com/mixelka/emailresend/internal/config"
	"github.com/mixelka/emailresend/internal/database"
	"github.com/mixelka/emailresend/internal/email"
	"github.com/mixelka/emailresend/internal/formatter"
	"github.com/mixelka/emailresend/internal/mailcow"
	"github.com/mixelka/emailresend/internal/parser"
	"github.com/mixelka/emailresend/internal/telegram"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Setup logger
	logger := setupLogger(cfg.LogLevel, cfg.LogFormat)
	logger.Info("starting email-to-telegram bot")

	// Connect to database
	db, err := database.New(cfg.DatabasePath)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Run migrations
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}
	logger.Info("database migrations completed")

	// Create components
	emailManager := email.NewManager(cfg, logger)
	htmlParser := parser.NewHTMLParser()
	codeDetector := parser.NewCodeDetector()
	tgFormatter := formatter.NewTelegramFormatter()

	// Create Mailcow client (optional)
	var mailcowClient *mailcow.Client
	if cfg.MailcowEnabled() {
		mailcowClient = mailcow.NewClient(mailcow.Config{
			BaseURL: cfg.MailcowURL,
			APIKey:  cfg.MailcowAPIKey,
			Domain:  cfg.MailcowDomain,
		})
		logger.Info("mailcow integration enabled", "domain", cfg.MailcowDomain)
	}

	// Create bot
	bot, err := telegram.NewBot(telegram.BotDeps{
		Config:       cfg,
		DB:           db,
		EmailManager: emailManager,
		Mailcow:      mailcowClient,
		HTMLParser:   htmlParser,
		CodeDetector: codeDetector,
		Formatter:    tgFormatter,
		Logger:       logger,
	})
	if err != nil {
		logger.Error("failed to create bot", "error", err)
		os.Exit(1)
	}

	// Setup email callbacks
	bot.SetupEmailCallbacks()

	// Restore email connections from database
	accounts, err := db.GetAllActiveAccounts(ctx)
	if err != nil {
		logger.Error("failed to get active accounts", "error", err)
		os.Exit(1)
	}

	if len(accounts) > 0 {
		logger.Info("restoring email connections", "count", len(accounts))
		emailManager.RestoreAll(ctx, accounts)
	}

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh

		logger.Info("received shutdown signal", "signal", sig)
		logger.Info("shutting down...")

		emailManager.StopAll()
		cancel()
	}()

	// Start bot
	logger.Info("bot is running, press Ctrl+C to stop")
	bot.Start(ctx)

	logger.Info("bot stopped")
}

func setupLogger(level, format string) *slog.Logger {
	var handler slog.Handler
	logLevel := parseLevel(level)

	if format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: logLevel,
		})
	} else {
		// Pretty colored output for console
		handler = tint.NewHandler(os.Stdout, &tint.Options{
			Level:      logLevel,
			TimeFormat: time.DateTime,
			NoColor:    false,
		})
	}

	return slog.New(handler)
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
