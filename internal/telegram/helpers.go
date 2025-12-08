package telegram

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// isUserAdmin checks if a user is an admin in the chat
func (b *Bot) isUserAdmin(ctx context.Context, chatID, userID int64) (bool, error) {
	// Use separate context with timeout to avoid blocking
	apiCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	b.logger.Info("calling GetChatMember API", "chat_id", chatID, "user_id", userID)
	member, err := b.bot.GetChatMember(apiCtx, &bot.GetChatMemberParams{
		ChatID: chatID,
		UserID: userID,
	})
	b.logger.Info("GetChatMember returned", "error", err)
	if err != nil {
		return false, err
	}

	b.logger.Info("member type", "type", member.Type)
	// Check member type
	switch member.Type {
	case models.ChatMemberTypeOwner, models.ChatMemberTypeAdministrator:
		return true, nil
	default:
		return false, nil
	}
}

// sendMessage sends a message to a topic
func (b *Bot) sendMessage(ctx context.Context, chatID int64, topicID int, text string) (*models.Message, error) {
	params := &bot.SendMessageParams{
		ChatID:    chatID,
		Text:      text,
		ParseMode: models.ParseModeHTML,
	}

	if topicID != 0 {
		params.MessageThreadID = topicID
	}

	return b.bot.SendMessage(ctx, params)
}

// sendMessageWithKeyboard sends a message with inline keyboard
func (b *Bot) sendMessageWithKeyboard(ctx context.Context, chatID int64, topicID int, text string, keyboard *models.InlineKeyboardMarkup) (*models.Message, error) {
	params := &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: keyboard,
	}

	if topicID != 0 {
		params.MessageThreadID = topicID
	}

	return b.bot.SendMessage(ctx, params)
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

// DecryptPasswordFunc returns a function for decrypting passwords
func (b *Bot) DecryptPasswordFunc() func(string) string {
	return func(encrypted string) string {
		decrypted, err := b.decryptPassword(encrypted)
		if err != nil {
			b.logger.Error("failed to decrypt password", "error", err)
			return ""
		}
		return decrypted
	}
}
