package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mixelka/emailresend/internal/database"
	"github.com/mixelka/emailresend/internal/email"
	"github.com/mixelka/emailresend/internal/formatter"
	"github.com/mixelka/emailresend/pkg/models"
)

// SetupEmailCallbacks sets up email message callbacks
func (b *Bot) SetupEmailCallbacks() {
	b.emailManager.SetMessageHandler(b.onNewEmail)
	b.emailManager.SetErrorHandler(b.onEmailError)
	b.emailManager.SetDecryptFunc(b.DecryptPasswordFunc())
}

// onNewEmail handles a new email message
func (b *Bot) onNewEmail(accountID int64, rawEmail *email.RawEmail) {
	ctx := context.Background()

	b.logger.Info("received new email",
		"account_id", accountID,
		"from", rawEmail.From.Address,
		"subject", rawEmail.Subject,
	)

	// Get account
	account, err := b.db.GetAccountByID(ctx, accountID)
	if err != nil {
		b.logger.Error("failed to get account", "error", err, "account_id", accountID)
		return
	}

	// Parse HTML to text
	bodyText := rawEmail.BodyText
	if rawEmail.BodyHTML != "" {
		parsed, err := b.htmlParser.Parse(rawEmail.BodyHTML)
		if err != nil {
			b.logger.Warn("failed to parse HTML", "error", err)
		} else {
			bodyText = parsed
		}
	}

	// Detect codes
	codes := b.codeDetector.DetectCodes(bodyText)
	b.logger.Debug("detected codes", "count", len(codes), "codes", codes)

	// Create message record
	codesJSON, _ := json.Marshal(codes)
	emailMsg := &models.EmailMessage{
		AccountID:     accountID,
		UID:           rawEmail.UID,
		MessageID:     rawEmail.MessageID,
		FromAddr:      rawEmail.From.Address,
		FromName:      rawEmail.From.Name,
		Subject:       rawEmail.Subject,
		BodyText:      bodyText,
		BodyHTML:      rawEmail.BodyHTML,
		ReceivedAt:    rawEmail.Date,
		DetectedCodes: string(codesJSON),
	}

	// Save to database
	if err := b.db.CreateMessage(ctx, emailMsg); err != nil {
		if errors.Is(err, database.ErrAlreadyExists) {
			// Message already exists, skip
			b.logger.Debug("message already exists, skipping", "uid", rawEmail.UID)
			return
		}
		b.logger.Error("failed to save message", "error", err)
		return
	}

	// Format for Telegram
	text := b.formatter.FormatEmail(emailMsg, codes)
	keyboard := formatter.BuildEmailKeyboard(emailMsg.ID, codes, false)

	// Send to topic
	tgMsg, err := b.sendMessageWithKeyboard(ctx, account.ChatID, account.TopicID, text, keyboard)
	if err != nil {
		b.logger.Error("failed to send to telegram", "error", err)
		return
	}

	// Update telegram message ID
	if err := b.db.UpdateMessageTelegramMsgID(ctx, emailMsg.ID, tgMsg.ID); err != nil {
		b.logger.Error("failed to update telegram msg id", "error", err)
	}

	// Update last UID
	if err := b.db.UpdateAccountLastUID(ctx, accountID, rawEmail.UID); err != nil {
		b.logger.Error("failed to update last uid", "error", err)
	}

	b.logger.Info("email sent to telegram",
		"account_id", accountID,
		"telegram_msg_id", tgMsg.ID,
		"codes_detected", len(codes),
	)
}

// onEmailError handles an email error
func (b *Bot) onEmailError(accountID int64, err error) {
	ctx := context.Background()

	b.logger.Error("email error", "account_id", accountID, "error", err)

	// Get account
	account, errDB := b.db.GetAccountByID(ctx, accountID)
	if errDB != nil {
		b.logger.Error("failed to get account for error notification", "error", errDB)
		return
	}

	// Send error notification to topic
	text := fmt.Sprintf("Ошибка подключения к почте <b>%s</b>:\n<code>%v</code>\n\nПопытка переподключения...",
		account.Email, err)
	b.sendMessage(ctx, account.ChatID, account.TopicID, text)
}
