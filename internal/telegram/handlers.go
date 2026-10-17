package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mixelka/emailresend/internal/database"
	"github.com/mixelka/emailresend/internal/email"
	"github.com/mixelka/emailresend/internal/formatter"
	"github.com/mixelka/emailresend/internal/mailcow"
	appmodels "github.com/mixelka/emailresend/pkg/models"
)

// handleConnect handles /connect command
// Usage: /connect email password [imap_server]
func (b *Bot) handleConnect(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	msg := update.Message

	if !b.requireForum(ctx, msg) ||
		!b.requireAdmin(ctx, msg, "Только администраторы могут подключать почтовые аккаунты") {
		return
	}

	// Parse command: /connect email password [imap_server]
	parts := strings.Fields(msg.Text)
	if len(parts) < 3 || len(parts) > 4 {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID,
			"Использование: <code>/connect email@example.com password</code>\nИли: <code>/connect email@example.com password imap.server.com:993</code>")
		return
	}

	emailAddr := strings.ToLower(parts[1])
	password := parts[2]
	topicID := msg.MessageThreadID

	// Delete the message with password immediately
	b.deletePasswordMessage(ctx, msg)

	if email.GetDomainFromEmail(emailAddr) == "" {
		b.sendMessage(ctx, msg.Chat.ID, topicID, "Некорректный email: "+escape(emailAddr))
		return
	}

	// Check if topic already has an account
	existing, err := b.db.GetAccountByChatAndTopic(ctx, msg.Chat.ID, topicID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		b.logger.Error("failed to check existing account", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, topicID, "Ошибка проверки существующего подключения")
		return
	}
	if existing != nil {
		b.sendMessage(ctx, msg.Chat.ID, topicID,
			fmt.Sprintf("В этом топике уже подключена почта: %s\nИспользуйте /disconnect для отключения", escape(existing.Email)))
		return
	}

	// One status message, updated as we go
	status := newStatusMessage(ctx, b, msg.Chat.ID, topicID)

	// Determine IMAP server
	var imapServer string
	if len(parts) == 4 {
		imapServer = parts[3]
		if !strings.Contains(imapServer, ":") {
			imapServer += ":993"
		}
	} else {
		status.set("Определяю IMAP сервер...")
		imapServer, err = email.ResolveIMAPServer(emailAddr)
		if err != nil {
			b.logger.Error("failed to resolve IMAP server", "error", err)
			status.set(fmt.Sprintf("Не удалось определить IMAP сервер для %s\nПопробуйте указать вручную: <code>/connect email password imap.server.com:993</code>", escape(emailAddr)))
			return
		}
		b.logger.Info("resolved IMAP server", "email", emailAddr, "server", imapServer)
	}

	// Test connection
	status.set(fmt.Sprintf("Проверяю подключение к %s...", escape(imapServer)))

	if err := b.emailManager.TestConnection(ctx, emailAddr, password, imapServer); err != nil {
		b.logger.Error("connection test failed", "email", emailAddr, "server", imapServer, "error", err)
		status.set(fmt.Sprintf("Ошибка подключения к %s:\n<code>%s</code>\n\nПроверьте пароль (для Gmail/Yandex/Mail.ru нужен пароль приложения) или укажите сервер вручную: <code>/connect email password imap.server.com:993</code>",
			escape(imapServer), escape(err.Error())))
		return
	}

	if b.saveAndStartAccount(ctx, status, emailAddr, password, imapServer, msg) == nil {
		return
	}

	status.set(fmt.Sprintf("Почта <b>%s</b> успешно подключена к этому топику!\nСервер: %s\n\nНовые письма будут автоматически пересылаться сюда.",
		escape(emailAddr), escape(imapServer)))
}

// saveAndStartAccount stores the account and starts its email client.
// On failure it reports to the status message and returns nil.
func (b *Bot) saveAndStartAccount(ctx context.Context, status *statusMessage, emailAddr, password, imapServer string, msg *models.Message) *appmodels.EmailAccount {
	encryptedPassword, err := b.encryptPassword(password)
	if err != nil {
		b.logger.Error("failed to encrypt password", "error", err)
		status.set("Ошибка шифрования пароля")
		return nil
	}

	account := &appmodels.EmailAccount{
		Email:      emailAddr,
		Password:   encryptedPassword,
		IMAPServer: imapServer,
		ChatID:     msg.Chat.ID,
		TopicID:    msg.MessageThreadID,
		IsActive:   true,
		CreatedBy:  senderID(msg),
	}

	if err := b.db.CreateAccount(ctx, account); err != nil {
		if errors.Is(err, database.ErrAlreadyExists) {
			status.set(fmt.Sprintf("Почта <b>%s</b> уже подключена к другому топику этой группы. Сначала выполните там /disconnect", escape(emailAddr)))
			return nil
		}
		b.logger.Error("failed to create account", "error", err)
		status.set("Ошибка сохранения аккаунта в базу данных")
		return nil
	}

	if err := b.emailManager.AddAccount(ctx, account); err != nil {
		b.logger.Error("failed to start email client", "error", err)
		b.db.DeleteAccount(ctx, account.ID)
		status.set(fmt.Sprintf("Ошибка запуска подключения: %s", escape(err.Error())))
		return nil
	}

	return account
}

// handleCreate handles /create command for Mailcow mailbox creation
// Usage: /create local_part [password] [name]
func (b *Bot) handleCreate(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	msg := update.Message

	// Check if Mailcow is configured
	if b.mailcow == nil || !b.mailcow.IsConfigured() {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Интеграция с Mailcow не настроена")
		return
	}

	if !b.requireForum(ctx, msg) ||
		!b.requireAdmin(ctx, msg, "Только администраторы могут создавать почтовые ящики") {
		return
	}

	// Parse command: /create local_part [password] [name]
	parts := strings.Fields(msg.Text)
	if len(parts) < 2 {
		domain := b.mailcow.GetDomain()
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID,
			fmt.Sprintf("Использование: <code>/create username</code>\nИли: <code>/create username password</code>\nИли: <code>/create username password Имя</code>\n\nБудет создан ящик: username@%s", escape(domain)))
		return
	}

	localPart := strings.ToLower(parts[1])
	if err := mailcow.ValidateLocalPart(localPart); err != nil {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, escape(err.Error()))
		return
	}
	password := ""
	name := localPart

	if len(parts) >= 3 {
		password = parts[2]
	}
	if len(parts) >= 4 {
		name = strings.Join(parts[3:], " ")
	}

	topicID := msg.MessageThreadID

	// Delete the message with password immediately (if password was provided)
	if len(parts) >= 3 {
		b.deletePasswordMessage(ctx, msg)
	}

	// Check if topic already has an account
	existing, err := b.db.GetAccountByChatAndTopic(ctx, msg.Chat.ID, topicID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		b.logger.Error("failed to check existing account", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, topicID, "Ошибка проверки существующего подключения")
		return
	}
	if existing != nil {
		b.sendMessage(ctx, msg.Chat.ID, topicID,
			fmt.Sprintf("В этом топике уже подключена почта: %s\nИспользуйте /disconnect для отключения", escape(existing.Email)))
		return
	}

	status := newStatusMessage(ctx, b, msg.Chat.ID, topicID)
	status.set("Создаю почтовый ящик...")

	mailbox, err := b.mailcow.CreateMailbox(ctx, localPart, name, password)
	if err != nil {
		b.logger.Error("failed to create mailbox", "error", err)
		status.set(fmt.Sprintf("Ошибка создания почтового ящика: %s", escape(err.Error())))
		return
	}

	emailAddr := mailbox.Email()
	imapServer := b.mailcow.GetIMAPServer()

	if b.saveAndStartAccount(ctx, status, emailAddr, mailbox.Password, imapServer, msg) == nil {
		// Do not leave an orphaned mailbox behind
		if err := b.mailcow.DeleteMailbox(ctx, emailAddr); err != nil {
			b.logger.Error("failed to delete mailbox after error", "email", emailAddr, "error", err)
		}
		return
	}

	host, _, _ := strings.Cut(imapServer, ":")
	status.set(fmt.Sprintf(
		"Почтовый ящик успешно создан!\n\n"+
			"<b>Email:</b> <code>%s</code>\n"+
			"<b>Пароль:</b> <code>%s</code>\n"+
			"<b>IMAP:</b> %s\n"+
			"<b>SMTP:</b> %s:587\n\n"+
			"Новые письма будут автоматически пересылаться в этот топик.",
		escape(emailAddr),
		escape(mailbox.Password),
		escape(imapServer),
		escape(host),
	))
}

// handleDisconnect handles /disconnect command
func (b *Bot) handleDisconnect(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	msg := update.Message

	if !b.requireForum(ctx, msg) ||
		!b.requireAdmin(ctx, msg, "Только администраторы могут отключать почтовые аккаунты") {
		return
	}

	topicID := msg.MessageThreadID

	// Get account
	account, err := b.db.GetAccountByChatAndTopic(ctx, msg.Chat.ID, topicID)
	if errors.Is(err, database.ErrNotFound) {
		b.sendMessage(ctx, msg.Chat.ID, topicID, "В этом топике нет подключенной почты")
		return
	}
	if err != nil {
		b.logger.Error("failed to get account", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, topicID, "Ошибка получения информации об аккаунте")
		return
	}

	// Stop email client
	if err := b.emailManager.RemoveAccount(account.ID); err != nil {
		b.logger.Error("failed to stop email client", "error", err)
	}

	// Delete from database
	if err := b.db.DeleteAccount(ctx, account.ID); err != nil {
		b.logger.Error("failed to delete account", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, topicID, "Ошибка удаления аккаунта")
		return
	}

	b.logger.Info("email disconnected", "email", account.Email, "chat_id", msg.Chat.ID, "topic_id", topicID)
	b.sendMessage(ctx, msg.Chat.ID, topicID,
		fmt.Sprintf("Почта <b>%s</b> отключена от этого топика", escape(account.Email)))
}

// handleStatus handles /status command
func (b *Bot) handleStatus(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	msg := update.Message

	// Get all accounts for this chat
	accounts, err := b.db.GetAccountsByChatID(ctx, msg.Chat.ID)
	if err != nil {
		b.logger.Error("failed to get accounts", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Ошибка получения списка аккаунтов")
		return
	}

	if len(accounts) == 0 {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "В этой группе нет подключенных почтовых аккаунтов")
		return
	}

	var sb strings.Builder
	sb.WriteString("<b>Подключенные почтовые аккаунты:</b>\n\n")

	for _, acc := range accounts {
		statusEmoji, statusText := "🔴", "отключена"
		switch b.emailManager.GetStatus(acc.ID) {
		case email.StatusConnected:
			statusEmoji, statusText = "🟢", "подключена"
		case email.StatusReconnecting:
			statusEmoji, statusText = "🟡", "переподключение"
		}

		sb.WriteString(fmt.Sprintf("%s <b>%s</b>\n", statusEmoji, escape(acc.Email)))
		sb.WriteString(fmt.Sprintf("   Топик ID: %d\n", acc.TopicID))
		sb.WriteString(fmt.Sprintf("   Сервер: %s\n", escape(acc.IMAPServer)))
		sb.WriteString(fmt.Sprintf("   Статус: %s\n\n", statusText))
	}

	b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, sb.String())
}

// handleCallback handles inline button callbacks
func (b *Bot) handleCallback(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	callback := update.CallbackQuery
	if callback == nil {
		return
	}

	data, err := formatter.DecodeCallback(callback.Data)
	if err != nil {
		b.logger.Warn("failed to decode callback", "error", err, "data", callback.Data)
		b.answerCallback(ctx, callback.ID, "Кнопка устарела", false)
		return
	}

	if data.Action == appmodels.CallbackCopyCode {
		b.handleCopyCode(ctx, callback, data)
		return
	}

	msg, account, ok := b.callbackTarget(ctx, callback, data)
	if !ok {
		return
	}

	switch data.Action {
	case appmodels.CallbackMarkRead:
		b.handleMarkRead(ctx, callback, msg, account)
	case appmodels.CallbackDelete:
		b.setEmailKeyboard(ctx, account, msg, formatter.BuildDeleteConfirmKeyboard(msg.ID, parseCodes(msg)))
		b.answerCallback(ctx, callback.ID, "Письмо будет удалено из почтового ящика. Подтвердите", false)
	case appmodels.CallbackDeleteCancel:
		b.setEmailKeyboard(ctx, account, msg, formatter.BuildEmailKeyboard(msg.ID, parseCodes(msg), msg.IsRead))
		b.answerCallback(ctx, callback.ID, "Отменено", false)
	case appmodels.CallbackDeleteConfirm:
		b.handleDelete(ctx, callback, msg, account)
	default:
		b.answerCallback(ctx, callback.ID, "Неизвестное действие", false)
	}
}

// callbackTarget loads the email and account of a button and checks that
// the user may act on it. It answers the callback itself when not ok.
func (b *Bot) callbackTarget(ctx context.Context, callback *models.CallbackQuery, data appmodels.CallbackData) (*appmodels.EmailMessage, *appmodels.EmailAccount, bool) {
	msg, err := b.db.GetMessageByID(ctx, data.MessageID)
	if err != nil {
		if !errors.Is(err, database.ErrNotFound) {
			b.logger.Error("failed to get message", "error", err)
		}
		b.answerCallback(ctx, callback.ID, "Письмо не найдено (почта отключена?)", false)
		return nil, nil, false
	}

	account, err := b.db.GetAccountByID(ctx, msg.AccountID)
	if err != nil {
		if !errors.Is(err, database.ErrNotFound) {
			b.logger.Error("failed to get account", "error", err)
		}
		b.answerCallback(ctx, callback.ID, "Почта отключена", false)
		return nil, nil, false
	}

	// The button must belong to this account's chat
	if callback.Message.Message != nil && callback.Message.Message.Chat.ID != account.ChatID {
		b.answerCallback(ctx, callback.ID, "Кнопка не относится к этому чату", false)
		return nil, nil, false
	}

	isAdmin, err := b.isChatAdmin(ctx, account.ChatID, callback.From.ID)
	if err != nil {
		b.logger.Error("failed to check admin status", "error", err)
		b.answerCallback(ctx, callback.ID, "Ошибка проверки прав", false)
		return nil, nil, false
	}
	if !isAdmin {
		b.answerCallback(ctx, callback.ID, "Только администраторы могут управлять письмами", true)
		return nil, nil, false
	}

	return msg, account, true
}

// handleMarkRead handles mark as read callback
func (b *Bot) handleMarkRead(ctx context.Context, callback *models.CallbackQuery, msg *appmodels.EmailMessage, account *appmodels.EmailAccount) {
	if err := b.emailManager.MarkAsRead(account.ID, msg.UID); err != nil {
		b.logger.Error("failed to mark as read", "error", err)
		b.answerCallback(ctx, callback.ID, "Ошибка: "+err.Error(), true)
		return
	}

	if err := b.db.MarkMessageAsRead(ctx, msg.ID); err != nil {
		b.logger.Error("failed to update message", "error", err)
	}

	b.setEmailKeyboard(ctx, account, msg, formatter.BuildEmailKeyboard(msg.ID, parseCodes(msg), true))
	b.answerCallback(ctx, callback.ID, "Помечено как прочитанное", false)
}

// handleDelete deletes the email from the mailbox and the chat
func (b *Bot) handleDelete(ctx context.Context, callback *models.CallbackQuery, msg *appmodels.EmailMessage, account *appmodels.EmailAccount) {
	if err := b.emailManager.DeleteMessage(account.ID, msg.UID); err != nil {
		b.logger.Error("failed to delete message", "error", err)
		b.answerCallback(ctx, callback.ID, "Ошибка: "+err.Error(), true)
		return
	}

	if err := b.db.MarkMessageAsDeleted(ctx, msg.ID); err != nil {
		b.logger.Error("failed to update message", "error", err)
	}

	if err := b.deleteMessage(ctx, account.ChatID, msg.TelegramMsgID); err != nil {
		// Telegram does not let bots delete messages older than 48 hours
		b.logger.Warn("failed to delete telegram message", "error", err)
		b.editMessageReplyMarkup(ctx, account.ChatID, msg.TelegramMsgID, &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{},
		})
	}

	b.answerCallback(ctx, callback.ID, "Письмо удалено", false)
}

// handleCopyCode handles buttons of messages sent by older versions
func (b *Bot) handleCopyCode(ctx context.Context, callback *models.CallbackQuery, data appmodels.CallbackData) {
	msg, err := b.db.GetMessageByID(ctx, data.MessageID)
	if err != nil {
		b.answerCallback(ctx, callback.ID, "Письмо не найдено", false)
		return
	}

	codes := parseCodes(msg)
	if data.CodeIndex < 0 || data.CodeIndex >= len(codes) {
		b.answerCallback(ctx, callback.ID, "Код не найден", false)
		return
	}
	b.answerCallback(ctx, callback.ID, fmt.Sprintf("Код: %s", codes[data.CodeIndex].Value), true)
}

func (b *Bot) setEmailKeyboard(ctx context.Context, account *appmodels.EmailAccount, msg *appmodels.EmailMessage, keyboard *models.InlineKeyboardMarkup) {
	if err := b.editMessageReplyMarkup(ctx, account.ChatID, msg.TelegramMsgID, keyboard); err != nil {
		b.logger.Warn("failed to update keyboard", "error", err)
	}
}

// parseCodes returns the codes stored with a message
func parseCodes(msg *appmodels.EmailMessage) []appmodels.DetectedCode {
	var codes []appmodels.DetectedCode
	if msg.DetectedCodes != "" {
		_ = json.Unmarshal([]byte(msg.DetectedCodes), &codes)
	}
	return codes
}

// deletePasswordMessage removes a command containing a password and warns
// if that is impossible
func (b *Bot) deletePasswordMessage(ctx context.Context, msg *models.Message) {
	if err := b.deleteMessage(ctx, msg.Chat.ID, msg.ID); err != nil {
		b.logger.Warn("failed to delete message with password", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID,
			"⚠️ Не удалось удалить сообщение с паролем. Удалите его вручную и дайте боту право «Удаление сообщений».")
	}
}

// statusMessage is a progress message that is edited instead of sending new ones
type statusMessage struct {
	ctx     context.Context
	b       *Bot
	chatID  int64
	topicID int
	msgID   int
}

func newStatusMessage(ctx context.Context, b *Bot, chatID int64, topicID int) *statusMessage {
	return &statusMessage{ctx: ctx, b: b, chatID: chatID, topicID: topicID}
}

func (s *statusMessage) set(text string) {
	if s.msgID != 0 {
		if err := s.b.editMessageText(s.ctx, s.chatID, s.msgID, text); err == nil {
			return
		}
	}
	if sent, err := s.b.sendMessage(s.ctx, s.chatID, s.topicID, text); err == nil {
		s.msgID = sent.ID
	}
}

// escape escapes text for Telegram HTML parse mode
func escape(s string) string {
	return html.EscapeString(s)
}
