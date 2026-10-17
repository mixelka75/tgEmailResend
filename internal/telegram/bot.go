package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"

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
		bot.WithMiddlewares(b.recoverMiddleware),
		bot.WithErrorsHandler(func(err error) {
			b.logger.Warn("telegram polling error", "error", err)
		}),
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
	b.bot.RegisterHandlerMatchFunc(matchCommand("connect"), b.handleConnect)
	b.bot.RegisterHandlerMatchFunc(matchCommand("create"), b.handleCreate)
	b.bot.RegisterHandlerMatchFunc(matchCommand("disconnect"), b.handleDisconnect)
	b.bot.RegisterHandlerMatchFunc(matchCommand("status"), b.handleStatus)
	b.bot.RegisterHandlerMatchFunc(matchCommand("start"), b.handleStart)
	b.bot.RegisterHandlerMatchFunc(matchCommand("help"), b.handleHelp)
	b.bot.RegisterHandler(bot.HandlerTypeCallbackQueryData, "", bot.MatchTypePrefix, b.handleCallback)
}

// matchCommand matches "/name", "/name@botname" and "/name args"
func matchCommand(name string) bot.MatchFunc {
	return func(update *models.Update) bool {
		if update.Message == nil {
			return false
		}
		fields := strings.Fields(update.Message.Text)
		if len(fields) == 0 {
			return false
		}
		cmd, _, _ := strings.Cut(fields[0], "@")
		return strings.EqualFold(cmd, "/"+name)
	}
}

// recoverMiddleware keeps a panic in one handler from crashing the whole bot
func (b *Bot) recoverMiddleware(next bot.HandlerFunc) bot.HandlerFunc {
	return func(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
		defer func() {
			if r := recover(); r != nil {
				b.logger.Error("panic in update handler", "panic", r, "stack", string(debug.Stack()))
			}
		}()
		next(ctx, tgBot, update)
	}
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

	// Check if it's a private chat
	if msg.Chat.Type == "private" {
		text := `<b>Email to Telegram Bot</b>

Бот для пересылки email сообщений в Telegram.

<b>Как использовать:</b>
1. Создайте супергруппу
2. Включите топики в настройках группы
3. Добавьте бота в группу
4. Сделайте бота администратором
5. В нужном топике используйте /connect

<b>Почему нужны топики?</b>
Каждый email-аккаунт привязывается к отдельному топику. Это позволяет удобно разделять письма от разных аккаунтов.

<b>Как включить топики:</b>
Настройки группы → Темы → Включить`

		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, text)
		return
	}

	// Check if it's a group without topics
	if msg.Chat.Type == "group" || (msg.Chat.Type == "supergroup" && !msg.Chat.IsForum) {
		text := `<b>Требуются топики!</b>

Этот бот работает только в супергруппах с включёнными топиками.

<b>Как включить:</b>
1. Откройте настройки группы
2. Найдите раздел "Темы" (Topics)
3. Включите топики

После этого каждый email можно будет привязать к отдельному топику.`

		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, text)
		return
	}

	// Normal help for supergroups with topics
	text := `<b>Email to Telegram Bot</b>

Пересылка email сообщений в этот топик.

<b>Команды:</b>
/connect email password — подключить почту
/disconnect — отключить почту
/status — статус подключений`

	// Add /create command info if Mailcow is configured
	if b.mailcow != nil && b.mailcow.IsConfigured() {
		text += fmt.Sprintf(`
/create username — создать ящик на %s`, b.mailcow.GetDomain())
	}

	text += `

<b>Примеры:</b>
<code>/connect user@gmail.com app_password</code>
<code>/connect user@mail.ru pass imap.mail.ru:993</code>

<b>Важно:</b>
• Только администраторы могут управлять почтами
• Для Gmail/Yandex нужен пароль приложения
• IMAP сервер определяется автоматически`

	b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, text)
}
