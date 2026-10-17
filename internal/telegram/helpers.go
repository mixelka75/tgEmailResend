package telegram

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// isUserAdmin checks if the sender of a message is an admin in the chat
func (b *Bot) isUserAdmin(ctx context.Context, msg *models.Message) (bool, error) {
	// Anonymous admin: the message is sent on behalf of the group itself
	if msg.SenderChat != nil && msg.SenderChat.ID == msg.Chat.ID {
		return true, nil
	}
	if msg.From == nil {
		return false, nil
	}
	return b.isChatAdmin(ctx, msg.Chat.ID, msg.From.ID)
}

// isChatAdmin checks if a user is an admin in the chat
func (b *Bot) isChatAdmin(ctx context.Context, chatID, userID int64) (bool, error) {
	// Use separate context with timeout to avoid blocking
	apiCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	member, err := b.bot.GetChatMember(apiCtx, &bot.GetChatMemberParams{
		ChatID: chatID,
		UserID: userID,
	})
	if err != nil {
		return false, err
	}

	switch member.Type {
	case models.ChatMemberTypeOwner, models.ChatMemberTypeAdministrator:
		return true, nil
	default:
		return false, nil
	}
}

// requireAdmin replies with an error and returns false if the sender is not an admin
func (b *Bot) requireAdmin(ctx context.Context, msg *models.Message, deniedText string) bool {
	isAdmin, err := b.isUserAdmin(ctx, msg)
	if err != nil {
		b.logger.Error("failed to check admin status", "error", err)
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Ошибка проверки прав")
		return false
	}
	if !isAdmin {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, deniedText)
		return false
	}
	return true
}

// senderID returns the ID of the user (or anonymous group) who sent the message
func senderID(msg *models.Message) int64 {
	if msg.From != nil {
		return msg.From.ID
	}
	if msg.SenderChat != nil {
		return msg.SenderChat.ID
	}
	return 0
}

// requireForum replies with an error and returns false if the chat is not a supergroup with topics
func (b *Bot) requireForum(ctx context.Context, msg *models.Message) bool {
	if msg.Chat.Type != models.ChatTypeSupergroup {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Эта команда работает только в супергруппах")
		return false
	}
	if !msg.Chat.IsForum {
		b.sendMessage(ctx, msg.Chat.ID, msg.MessageThreadID, "Эта команда работает только в супергруппах с топиками")
		return false
	}
	return true
}

// sendMessage sends a message to a topic
func (b *Bot) sendMessage(ctx context.Context, chatID int64, topicID int, text string) (*models.Message, error) {
	return b.sendMessageWithKeyboard(ctx, chatID, topicID, text, nil)
}

// sendMessageWithKeyboard sends a message with inline keyboard
func (b *Bot) sendMessageWithKeyboard(ctx context.Context, chatID int64, topicID int, text string, keyboard *models.InlineKeyboardMarkup) (*models.Message, error) {
	params := &bot.SendMessageParams{
		ChatID:          chatID,
		MessageThreadID: topicID,
		Text:            text,
		ParseMode:       models.ParseModeHTML,
		LinkPreviewOptions: &models.LinkPreviewOptions{
			IsDisabled: bot.True(),
		},
	}
	if keyboard != nil {
		params.ReplyMarkup = keyboard
	}

	var sent *models.Message
	err := withRetry(ctx, func() error {
		var err error
		sent, err = b.bot.SendMessage(ctx, params)
		return err
	})
	if err != nil {
		b.logger.Error("failed to send message", "chat_id", chatID, "topic_id", topicID, "error", err)
	}
	return sent, err
}

// editMessageText replaces the text of a message
func (b *Bot) editMessageText(ctx context.Context, chatID int64, msgID int, text string) error {
	_, err := b.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: msgID,
		Text:      text,
		ParseMode: models.ParseModeHTML,
	})
	return err
}

// withRetry retries fn when Telegram answers 429 Too Many Requests
func withRetry(ctx context.Context, fn func() error) error {
	for attempt := 0; ; attempt++ {
		err := fn()
		var tooMany *bot.TooManyRequestsError
		if err == nil || !errors.As(err, &tooMany) || attempt >= 5 {
			return err
		}
		wait := time.Duration(tooMany.RetryAfter+1) * time.Second
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
	}
}

// deleteMessage deletes a message
func (b *Bot) deleteMessage(ctx context.Context, chatID int64, msgID int) error {
	_, err := b.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{
		ChatID:    chatID,
		MessageID: msgID,
	})
	return err
}

// editMessageReplyMarkup edits the reply markup of a message
func (b *Bot) editMessageReplyMarkup(ctx context.Context, chatID int64, msgID int, keyboard *models.InlineKeyboardMarkup) error {
	_, err := b.bot.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
		ChatID:      chatID,
		MessageID:   msgID,
		ReplyMarkup: keyboard,
	})
	return err
}

// answerCallback answers a callback query
func (b *Bot) answerCallback(ctx context.Context, callbackID, text string, showAlert bool) error {
	_, err := b.bot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: callbackID,
		Text:            text,
		ShowAlert:       showAlert,
	})
	return err
}

// encryptPassword encrypts a password using AES-256-GCM
func (b *Bot) encryptPassword(password string) (string, error) {
	key := []byte(b.config.EncryptionKey)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(password), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decryptPassword decrypts a password
func (b *Bot) decryptPassword(encrypted string) (string, error) {
	key := []byte(b.config.EncryptionKey)

	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", fmt.Errorf("failed to decode: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}
