package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Config application configuration
type Config struct {
	// Telegram
	TelegramToken string `env:"TELEGRAM_BOT_TOKEN,required"`
	// Local Bot API server, e.g. http://localhost:8081 (optional)
	TelegramAPIURL string `env:"TELEGRAM_API_URL"`
	// Send emails as rich messages (Bot API 10.1+); falls back to HTML automatically
	TelegramRichMessages bool `env:"TELEGRAM_RICH_MESSAGES" envDefault:"true"`

	// Database
	DatabasePath string `env:"DATABASE_PATH" envDefault:"./data/emailbot.db"`

	// Email
	IMAPIdleTimeout   time.Duration `env:"IMAP_IDLE_TIMEOUT" envDefault:"25m"`
	IMAPDialTimeout   time.Duration `env:"IMAP_DIAL_TIMEOUT" envDefault:"30s"`
	EmailPollInterval time.Duration `env:"EMAIL_POLL_INTERVAL" envDefault:"1m"`

	// Mailcow integration (optional)
	MailcowURL    string `env:"MAILCOW_URL"` // e.g., https://mail.example.com
	MailcowAPIKey string `env:"MAILCOW_API_KEY"`
	MailcowDomain string `env:"MAILCOW_DOMAIN"` // e.g., example.com
	// IMAP server of created mailboxes, host:port (default: MAILCOW_URL host, port 993)
	MailcowIMAPServer string `env:"MAILCOW_IMAP_SERVER"`
	// Quota of created mailboxes in MB (default: the domain's default quota)
	MailcowQuotaMB int `env:"MAILCOW_MAILBOX_QUOTA_MB"`

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

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	c.TelegramToken = strings.TrimSpace(c.TelegramToken)
	if !strings.Contains(c.TelegramToken, ":") {
		return errors.New("TELEGRAM_BOT_TOKEN looks invalid, expected the token from @BotFather like 123456:ABC-DEF")
	}

	// 32 bytes for AES-256
	if len(c.EncryptionKey) != 32 {
		return fmt.Errorf("ENCRYPTION_KEY must be exactly 32 bytes, got %d (generate one with: make generate-key)", len(c.EncryptionKey))
	}

	if c.EmailPollInterval < 10*time.Second {
		return fmt.Errorf("EMAIL_POLL_INTERVAL must be at least 10s, got %s", c.EmailPollInterval)
	}
	if c.IMAPIdleTimeout < time.Minute || c.IMAPIdleTimeout > 29*time.Minute {
		return fmt.Errorf("IMAP_IDLE_TIMEOUT must be between 1m and 29m, got %s", c.IMAPIdleTimeout)
	}
	if c.IMAPDialTimeout < time.Second {
		return fmt.Errorf("IMAP_DIAL_TIMEOUT must be at least 1s, got %s", c.IMAPDialTimeout)
	}

	for name, raw := range map[string]string{"TELEGRAM_API_URL": c.TelegramAPIURL, "MAILCOW_URL": c.MailcowURL} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%s must be an http(s) URL, got %q", name, raw)
		}
	}
	c.TelegramAPIURL = strings.TrimRight(c.TelegramAPIURL, "/")
	c.MailcowDomain = strings.ToLower(strings.TrimSpace(c.MailcowDomain))

	if c.MailcowQuotaMB < 0 {
		return fmt.Errorf("MAILCOW_MAILBOX_QUOTA_MB must not be negative, got %d", c.MailcowQuotaMB)
	}

	return nil
}
