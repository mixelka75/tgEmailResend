package telegram

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mixelka/emailresend/internal/config"
	"github.com/mixelka/emailresend/internal/database"
	"github.com/mixelka/emailresend/internal/email"
	"github.com/mixelka/emailresend/internal/formatter"
	"github.com/mixelka/emailresend/internal/mailcow"
	"github.com/mixelka/emailresend/internal/parser"
)

// Bot represents the Telegram bot
type Bot struct {
	bot          *bot.Bot
	db           *database.DB
	emailManager *email.Manager
	mailcow      *mailcow.Client
	htmlParser   *parser.HTMLParser
	codeDetector *parser.CodeDetector
	formatter    *formatter.TelegramFormatter
	logger       *slog.Logger
	config       *config.Config
}

// BotDeps dependencies for creating a bot
type BotDeps struct {
	Config       *config.Config
	DB           *database.DB
	EmailManager *email.Manager
	Mailcow      *mailcow.Client
	HTMLParser   *parser.HTMLParser
	CodeDetector *parser.CodeDetector
	Formatter    *formatter.TelegramFormatter
	Logger       *slog.Logger
}

// NewBot creates a new Telegram bot
func NewBot(deps BotDeps) (*Bot, error) {
	b := &Bot{
		db:           deps.DB,
		emailManager: deps.EmailManager,
		mailcow:      deps.Mailcow,
		htmlParser:   deps.HTMLParser,
		codeDetector: deps.CodeDetector,
		formatter:    deps.Formatter,
		logger:       deps.Logger.With("component", "telegram_bot"),
		config:       deps.Config,
	}

	opts := []bot.Option{
		bot.WithDefaultHandler(b.defaultHandler),
	}

	tgBot, err := bot.New(deps.Config.TelegramToken, opts...)
	if err != nil {
		return nil, err
	}

	b.bot = tgBot
	b.registerHandlers()

	return b, nil
}

// registerHandlers registers command handlers
func (b *Bot) registerHandlers() {
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/connect", bot.MatchTypePrefix, b.handleConnect)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/create", bot.MatchTypePrefix, b.handleCreate)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/disconnect", bot.MatchTypePrefix, b.handleDisconnect)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/status", bot.MatchTypePrefix, b.handleStatus)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypePrefix, b.handleStart)
	b.bot.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypePrefix, b.handleHelp)
	b.bot.RegisterHandler(bot.HandlerTypeCallbackQueryData, "", bot.MatchTypePrefix, b.handleCallback)
}

// Start starts the bot
func (b *Bot) Start(ctx context.Context) {
	b.logger.Info("starting telegram bot")
	b.bot.Start(ctx)
}

// defaultHandler handles unknown messages
func (b *Bot) defaultHandler(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	// Ignore non-message updates and messages without text
	if update.Message == nil {
		return
	}

	// Log unknown commands
	if update.Message.Text != "" && update.Message.Text[0] == '/' {
		b.logger.Debug("unknown command", "text", update.Message.Text)
	}
}

// handleStart handles /start command
func (b *Bot) handleStart(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	b.handleHelp(ctx, tgBot, update)
}

// handleHelp handles /help command
func (b *Bot) handleHelp(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	msg := update.Message

	text := `<b>Email to Telegram Bot</b>

Бот для пересылки email сообщений в Telegram топики.

<b>Команды:</b>
/connect email password - подключить существующую почту
/disconnect - отключить почту от топика
/status - показать статус подключений`

	// Add /create command info if Mailcow is configured
	if b.mailcow != nil && b.mailcow.IsConfigured() {
		text += fmt.Sprintf(`

<b>Mailcow:</b>
/create username - создать новый ящик на %s`, b.mailcow.GetDomain())
	}

	text += `

<b>Примеры:</b>
<code>/connect myemail@gmail.com mypassword</code>
<code>/connect myemail@mail.ru password imap.mail.ru:993</code>

<b>Важно:</b>
- Используйте в супергруппе с топиками
- Только администраторы могут управлять почтами
- Для Gmail используйте пароль приложения
- IMAP сервер определяется автоматически`

	b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, text)
}
