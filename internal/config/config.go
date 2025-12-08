package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Config application configuration
type Config struct {
	// Telegram
	TelegramToken string `env:"TELEGRAM_BOT_TOKEN,required"`

	// Database
	DatabasePath string `env:"DATABASE_PATH" envDefault:"./data/emailbot.db"`

	// Email
	IMAPIdleTimeout   time.Duration `env:"IMAP_IDLE_TIMEOUT" envDefault:"25m"`
	IMAPDialTimeout   time.Duration `env:"IMAP_DIAL_TIMEOUT" envDefault:"30s"`
	EmailPollInterval time.Duration `env:"EMAIL_POLL_INTERVAL" envDefault:"1m"`

	// Mailcow integration (optional)
	MailcowURL    string `env:"MAILCOW_URL"`    // e.g., https://mail.example.com
	MailcowAPIKey string `env:"MAILCOW_API_KEY"`
	MailcowDomain string `env:"MAILCOW_DOMAIN"` // e.g., example.com

	// Security
	EncryptionKey string `env:"ENCRYPTION_KEY,required"`

	// Logging
	LogLevel  string `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat string `env:"LOG_FORMAT" envDefault:"text"` // "json" or "text"
}

// MailcowEnabled returns true if Mailcow integration is configured
func (c *Config) MailcowEnabled() bool {
	return c.MailcowURL != "" && c.MailcowAPIKey != "" && c.MailcowDomain != ""
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Load .env file if exists (ignore error if not found)
	_ = godotenv.Load()

	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Validate encryption key length (32 bytes for AES-256)
	if len(cfg.EncryptionKey) != 32 {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be exactly 32 bytes, got %d", len(cfg.EncryptionKey))
	}

	return cfg, nil
}
