package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/mixelka/emailresend/internal/database"
	"github.com/mixelka/emailresend/internal/email"
	"github.com/mixelka/emailresend/internal/formatter"
	appmodels "github.com/mixelka/emailresend/pkg/models"
)

// handleConnect handles /connect command
// Usage: /connect email password [imap_server]
func (b *Bot) handleConnect(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	msg := update.Message

	// Check if it's a supergroup with topics
	if msg.Chat.Type != "supergroup" {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Эта команда работает только в супергруппах")
		return
	}

	if !msg.Chat.IsForum {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Эта команда работает только в супергруппах с топиками")
		return
	}

	// Check if user is admin
	isAdmin, err := b.isUserAdmin(ctx, msg.Chat.ID, msg.From.ID)
	if err != nil {
		b.logger.Error("failed to check admin status", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Ошибка проверки прав")
		return
	}

	if !isAdmin {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Только администраторы могут подключать почтовые аккаунты")
		return
	}

	// Parse command: /connect email password [imap_server]
	parts := strings.Fields(msg.Text)
	if len(parts) < 3 || len(parts) > 4 {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID,
			"Использование: <code>/connect email@example.com password</code>\nИли: <code>/connect email@example.com password imap.server.com:993</code>")
		return
	}

	emailAddr := parts[1]
	password := parts[2]
	topicID := msg.MessageThreadID

	// Delete the message with password immediately
	if err := b.deleteMessage(ctx, msg.Chat.ID, msg.ID); err != nil {
		b.logger.Warn("failed to delete connect message", "error", err)
	}

	// Determine IMAP server
	var imapServer string
	if len(parts) == 4 {
		// User specified server
		imapServer = parts[3]
	} else {
		// Auto-detect
		b.sendMessage(ctx, msg.Chat.ID, topicID, "Определяю IMAP сервер...")
		var err error
		imapServer, err = email.ResolveIMAPServer(emailAddr)
		if err != nil {
			b.logger.Error("failed to resolve IMAP server", "error", err)
			b.sendMessage(ctx, msg.Chat.ID, topicID,
				fmt.Sprintf("Не удалось определить IMAP сервер для %s\nПопробуйте указать вручную: <code>/connect email password imap.server.com:993</code>", emailAddr))
			return
		}
		b.logger.Info("resolved IMAP server", "email", emailAddr, "server", imapServer)
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
			fmt.Sprintf("В этом топике уже подключена почта: %s\nИспользуйте /disconnect для отключения", existing.Email))
		return
	}

	// Test connection
	b.sendMessage(ctx, msg.Chat.ID, topicID, fmt.Sprintf("Проверяю подключение к %s...", imapServer))

	if err := b.emailManager.TestConnection(ctx, emailAddr, password, imapServer); err != nil {
		b.logger.Error("connection test failed", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, topicID, fmt.Sprintf("Ошибка подключения: %v", err))
		return
	}

	// Encrypt password
	encryptedPassword, err := b.encryptPassword(password)
	if err != nil {
		b.logger.Error("failed to encrypt password", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, topicID, "Ошибка шифрования пароля")
		return
	}

	// Create account
	account := &appmodels.EmailAccount{
		Email:      emailAddr,
		Password:   encryptedPassword,
		IMAPServer: imapServer,
		ChatID:     msg.Chat.ID,
		TopicID:    topicID,
		IsActive:   true,
		CreatedBy:  msg.From.ID,
	}

	if err := b.db.CreateAccount(ctx, account); err != nil {
		b.logger.Error("failed to create account", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, topicID, "Ошибка сохранения аккаунта в базу данных")
		return
	}

	// Start email client
	if err := b.emailManager.AddAccount(ctx, account); err != nil {
		b.logger.Error("failed to start email client", "error", err)
		b.db.DeleteAccount(ctx, account.ID)
		b.sendMessage(ctx, msg.Chat.ID, topicID, fmt.Sprintf("Ошибка запуска подключения: %v", err))
		return
	}

	b.sendMessage(ctx, msg.Chat.ID, topicID,
		fmt.Sprintf("Почта <b>%s</b> успешно подключена к этому топику!\nСервер: %s\n\nНовые письма будут автоматически пересылаться сюда.", emailAddr, imapServer))
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

	// Check if it's a supergroup with topics
	if msg.Chat.Type != "supergroup" {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Эта команда работает только в супергруппах")
		return
	}

	if !msg.Chat.IsForum {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Эта команда работает только в супергруппах с топиками")
		return
	}

	// Check if user is admin
	isAdmin, err := b.isUserAdmin(ctx, msg.Chat.ID, msg.From.ID)
	if err != nil {
		b.logger.Error("failed to check admin status", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Ошибка проверки прав")
		return
	}

	if !isAdmin {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Только администраторы могут создавать почтовые ящики")
		return
	}

	// Parse command: /create local_part [password] [name]
	parts := strings.Fields(msg.Text)
	if len(parts) < 2 {
		domain := b.mailcow.GetDomain()
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID,
			fmt.Sprintf("Использование: <code>/create username</code>\nИли: <code>/create username password</code>\nИли: <code>/create username password Имя</code>\n\nБудет создан ящик: username@%s", domain))
		return
	}

	localPart := parts[1]
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
		if err := b.deleteMessage(ctx, msg.Chat.ID, msg.ID); err != nil {
			b.logger.Warn("failed to delete create message", "error", err)
		}
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
			fmt.Sprintf("В этом топике уже подключена почта: %s\nИспользуйте /disconnect для отключения", existing.Email))
		return
	}

	// Create mailbox in Mailcow
	b.sendMessage(ctx, msg.Chat.ID, topicID, "Создаю почтовый ящик...")

	mailbox, err := b.mailcow.CreateMailbox(ctx, localPart, name, password, 1024)
	if err != nil {
		b.logger.Error("failed to create mailbox", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, topicID, fmt.Sprintf("Ошибка создания почтового ящика: %v", err))
		return
	}

	emailAddr := mailbox.LocalPart + "@" + mailbox.Domain
	imapServer := b.mailcow.GetIMAPServer()

	// Encrypt password
	encryptedPassword, err := b.encryptPassword(mailbox.Password)
	if err != nil {
		b.logger.Error("failed to encrypt password", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, topicID, "Ошибка шифрования пароля")
		return
	}

	// Create account in database
	account := &appmodels.EmailAccount{
		Email:      emailAddr,
		Password:   encryptedPassword,
		IMAPServer: imapServer,
		ChatID:     msg.Chat.ID,
		TopicID:    topicID,
		IsActive:   true,
		CreatedBy:  msg.From.ID,
	}

	if err := b.db.CreateAccount(ctx, account); err != nil {
		b.logger.Error("failed to create account", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, topicID, "Ошибка сохранения аккаунта в базу данных")
		return
	}

	// Start email client
	if err := b.emailManager.AddAccount(ctx, account); err != nil {
		b.logger.Error("failed to start email client", "error", err)
		b.db.DeleteAccount(ctx, account.ID)
		b.sendMessage(ctx, msg.Chat.ID, topicID, fmt.Sprintf("Ошибка запуска подключения: %v", err))
		return
	}

	// Send success message with credentials
	credentialsMsg := fmt.Sprintf(
		"Почтовый ящик успешно создан!\n\n"+
			"<b>Email:</b> <code>%s</code>\n"+
			"<b>Пароль:</b> <code>%s</code>\n"+
			"<b>IMAP:</b> %s\n"+
			"<b>SMTP:</b> %s\n\n"+
			"Новые письма будут автоматически пересылаться в этот топик.",
		emailAddr,
		mailbox.Password,
		imapServer,
		strings.Replace(imapServer, ":993", ":587", 1),
	)
	b.sendMessage(ctx, msg.Chat.ID, topicID, credentialsMsg)
}

// handleDisconnect handles /disconnect command
func (b *Bot) handleDisconnect(ctx context.Context, tgBot *bot.Bot, update *models.Update) {
	msg := update.Message

	// Check if user is admin
	isAdmin, err := b.isUserAdmin(ctx, msg.Chat.ID, msg.From.ID)
	if err != nil {
		b.logger.Error("failed to check admin status", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Ошибка проверки прав")
		return
	}

	if !isAdmin {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Только администраторы могут отключать почтовые аккаунты")
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
		fmt.Sprintf("Почта <b>%s</b> отключена от этого топика", account.Email))
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
		status := b.emailManager.GetStatus(acc.ID)
		statusEmoji := "🔴"
		if status == "connected" {
			statusEmoji = "🟢"
		} else if status == "reconnecting" {
			statusEmoji = "🟡"
		}

		sb.WriteString(fmt.Sprintf("%s <b>%s</b>\n", statusEmoji, acc.Email))
		sb.WriteString(fmt.Sprintf("   Топик ID: %d\n", acc.TopicID))
		sb.WriteString(fmt.Sprintf("   Статус: %s\n\n", status))
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
		b.logger.Error("failed to decode callback", "error", err, "data", callback.Data)
		b.answerCallback(ctx, callback.ID, "Ошибка", false)
		return
	}

	switch data.Action {
	case appmodels.CallbackMarkRead:
		b.handleMarkRead(ctx, callback, data)
	case appmodels.CallbackDelete:
		b.handleDelete(ctx, callback, data)
	case appmodels.CallbackCopyCode:
		b.handleCopyCode(ctx, callback, data)
	default:
		b.answerCallback(ctx, callback.ID, "Неизвестное действие", false)
	}
}

// handleMarkRead handles mark as read callback
func (b *Bot) handleMarkRead(ctx context.Context, callback *models.CallbackQuery, data appmodels.CallbackData) {
	// Get message from database
	msg, err := b.db.GetMessageByID(ctx, data.MessageID)
	if err != nil {
		b.logger.Error("failed to get message", "error", err)
		b.answerCallback(ctx, callback.ID, "Сообщение не найдено", false)
		return
	}

	// Get account
	account, err := b.db.GetAccountByID(ctx, msg.AccountID)
	if err != nil {
		b.logger.Error("failed to get account", "error", err)
		b.answerCallback(ctx, callback.ID, "Аккаунт не найден", false)
		return
	}

	// Mark as read in IMAP
	if err := b.emailManager.MarkAsRead(account.ID, msg.UID); err != nil {
		b.logger.Error("failed to mark as read", "error", err)
		b.answerCallback(ctx, callback.ID, "Ошибка: "+err.Error(), false)
		return
	}

	// Update database
	if err := b.db.MarkMessageAsRead(ctx, msg.ID); err != nil {
		b.logger.Error("failed to update message", "error", err)
	}

	// Update keyboard
	var codes []appmodels.DetectedCode
	if msg.DetectedCodes != "" {
		// Parse codes (simplified, assuming JSON)
		// In real implementation, unmarshal JSON
	}
	keyboard := formatter.BuildEmailKeyboard(msg.ID, codes, true)
	b.editMessageReplyMarkup(ctx, account.ChatID, msg.TelegramMsgID, keyboard)

	b.answerCallback(ctx, callback.ID, "Помечено как прочитанное", false)
}

// handleDelete handles delete callback
func (b *Bot) handleDelete(ctx context.Context, callback *models.CallbackQuery, data appmodels.CallbackData) {
	// Get message from database
	msg, err := b.db.GetMessageByID(ctx, data.MessageID)
	if err != nil {
		b.logger.Error("failed to get message", "error", err)
		b.answerCallback(ctx, callback.ID, "Сообщение не найдено", false)
		return
	}

	// Get account
	account, err := b.db.GetAccountByID(ctx, msg.AccountID)
	if err != nil {
		b.logger.Error("failed to get account", "error", err)
		b.answerCallback(ctx, callback.ID, "Аккаунт не найден", false)
		return
	}

	// Delete from IMAP
	if err := b.emailManager.DeleteMessage(account.ID, msg.UID); err != nil {
		b.logger.Error("failed to delete message", "error", err)
		b.answerCallback(ctx, callback.ID, "Ошибка: "+err.Error(), false)
		return
	}

	// Update database
	if err := b.db.MarkMessageAsDeleted(ctx, msg.ID); err != nil {
		b.logger.Error("failed to update message", "error", err)
	}

	// Delete Telegram message
	b.deleteMessage(ctx, account.ChatID, msg.TelegramMsgID)

	b.answerCallback(ctx, callback.ID, "Письмо удалено", false)
}

// handleCopyCode handles copy code callback
func (b *Bot) handleCopyCode(ctx context.Context, callback *models.CallbackQuery, data appmodels.CallbackData) {
	// Get message from database
	msg, err := b.db.GetMessageByID(ctx, data.MessageID)
	if err != nil {
		b.logger.Error("failed to get message", "error", err)
		b.answerCallback(ctx, callback.ID, "Сообщение не найдено", false)
		return
	}

	// Parse codes
	codes := b.codeDetector.DetectCodes(msg.BodyText)
	if data.CodeIndex >= len(codes) {
		b.answerCallback(ctx, callback.ID, "Код не найден", false)
		return
	}

	code := codes[data.CodeIndex]
	// Show alert with code (can be copied)
	b.answerCallback(ctx, callback.ID, fmt.Sprintf("Код: %s", code.Value), true)
}
