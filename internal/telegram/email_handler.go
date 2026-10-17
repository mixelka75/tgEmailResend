package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	tgmodels "github.com/go-telegram/bot/models"

	"github.com/mixelka/emailresend/internal/database"
	"github.com/mixelka/emailresend/internal/email"
	"github.com/mixelka/emailresend/internal/formatter"
	"github.com/mixelka/emailresend/pkg/models"
)

// SetupEmailCallbacks sets up email message callbacks
func (b *Bot) SetupEmailCallbacks() {
	b.emailManager.SetMessageHandler(b.onNewEmail)
	b.emailManager.SetErrorHandler(b.onEmailError)
	b.emailManager.SetRecoverHandler(b.onEmailRecovered)
	b.emailManager.SetSyncHandler(b.onSync)
	b.emailManager.SetDecryptFunc(b.decryptPassword)
}

// onNewEmail delivers a new email to its topic. A returned error means
// delivery should be retried later.
func (b *Bot) onNewEmail(accountID int64, rawEmail *email.RawEmail) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	b.logger.Info("received new email",
		"account_id", accountID,
		"uid", rawEmail.UID,
		"from", rawEmail.From.Address,
		"subject", rawEmail.Subject,
	)

	account, err := b.db.GetAccountByID(ctx, accountID)
	if errors.Is(err, database.ErrNotFound) {
		return nil // disconnected meanwhile
	}
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}

	// Parse HTML to text (used for code detection and plain fallbacks)
	bodyText := rawEmail.BodyText
	if rawEmail.BodyHTML != "" {
		if parsed, err := b.htmlParser.Parse(rawEmail.BodyHTML); err != nil {
			b.logger.Warn("failed to parse HTML", "error", err)
		} else if parsed != "" {
			bodyText = parsed
		}
	}

	receivedAt := rawEmail.Date
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}

	// Detect codes (the subject often contains the code too)
	codes := b.codeDetector.DetectCodes(rawEmail.Subject + "\n" + bodyText)

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
		ReceivedAt:    receivedAt,
		DetectedCodes: string(codesJSON),
	}

	if err := b.db.CreateMessage(ctx, emailMsg); err != nil {
		if errors.Is(err, database.ErrAlreadyExists) {
			b.logger.Debug("message already delivered, skipping", "uid", rawEmail.UID)
			return nil
		}
		return fmt.Errorf("failed to save message: %w", err)
	}

	sent, err := b.deliverEmail(ctx, account, emailMsg, codes)
	if err != nil {
		if isPermanentError(err) {
			// Retrying will not help (bot removed from the group, no rights...)
			b.logger.Error("email cannot be delivered to telegram, skipping",
				"account_id", accountID, "chat_id", account.ChatID, "uid", rawEmail.UID, "error", err)
			b.persistLastUID(ctx, accountID, rawEmail.UID)
			return nil
		}
		// Forget the record so the retry is not treated as a duplicate
		if errDel := b.db.DeleteMessage(ctx, emailMsg.ID); errDel != nil {
			b.logger.Error("failed to delete undelivered message", "error", errDel)
		}
		return err
	}

	if err := b.db.UpdateMessageTelegramMsgID(ctx, emailMsg.ID, sent.ID); err != nil {
		b.logger.Error("failed to update telegram msg id", "error", err)
	}
	b.persistLastUID(ctx, accountID, rawEmail.UID)

	b.logger.Info("email sent to telegram",
		"account_id", accountID,
		"telegram_msg_id", sent.ID,
		"codes_detected", len(codes),
	)
	return nil
}

func (b *Bot) persistLastUID(ctx context.Context, accountID int64, uid uint32) {
	if err := b.db.UpdateAccountLastUID(ctx, accountID, uid); err != nil {
		b.logger.Error("failed to update last uid", "error", err)
	}
}

// deliverEmail sends an email to the account's topic. If the topic was
// deleted, the email goes to the general topic with a notice.
func (b *Bot) deliverEmail(ctx context.Context, account *models.EmailAccount, msg *models.EmailMessage, codes []models.DetectedCode) (*tgmodels.Message, error) {
	sent, err := b.sendEmail(ctx, account.ChatID, account.TopicID, msg, codes, "")
	if err != nil && account.TopicID != 0 && isThreadNotFound(err) {
		b.logger.Warn("topic not found, sending to the general topic", "chat_id", account.ChatID, "topic_id", account.TopicID)
		notice := fmt.Sprintf("Топик почты %s удалён. Подключите почту заново в нужном топике: /disconnect, затем /connect", account.Email)
		sent, err = b.sendEmail(ctx, account.ChatID, 0, msg, codes, notice)
	}
	return sent, err
}

// sendEmail tries a rich message first, then classic HTML, then plain text
func (b *Bot) sendEmail(ctx context.Context, chatID int64, topicID int, msg *models.EmailMessage, codes []models.DetectedCode, notice string) (*tgmodels.Message, error) {
	keyboard := formatter.BuildEmailKeyboard(msg.ID, codes, false)

	if b.richEnabled() {
		html := b.formatter.RichEmail(msg, codes)
		if notice != "" {
			html = "<p><i>" + escape(notice) + "</i></p>" + html
		}
		var sent *tgmodels.Message
		err := withRetry(ctx, func() error {
			var err error
			sent, err = b.bot.SendRichMessage(ctx, &bot.SendRichMessageParams{
				ChatID:          chatID,
				MessageThreadID: topicID,
				RichMessage:     tgmodels.InputRichMessage{HTML: html},
				ReplyMarkup:     keyboard,
			})
			return err
		})
		if err == nil {
			return sent, nil
		}
		if !isFormatError(err) {
			return nil, err
		}
		b.logger.Warn("rich message rejected, falling back to HTML", "error", err)
		if errors.Is(err, bot.ErrorNotFound) {
			// Bot API server without rich messages (old local server)
			b.disableRich()
		}
	}

	text := b.formatter.FormatEmail(msg, codes)
	if notice != "" {
		text = "<i>" + escape(notice) + "</i>\n\n" + text
	}
	sent, err := b.sendMessageWithKeyboard(ctx, chatID, topicID, text, keyboard)
	if err == nil || !isFormatError(err) {
		return sent, err
	}

	b.logger.Warn("HTML message rejected, falling back to plain text", "error", err)
	plain := b.formatter.PlainEmail(msg)
	if notice != "" {
		plain = notice + "\n\n" + plain
	}
	err = withRetry(ctx, func() error {
		var err error
		sent, err = b.bot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          chatID,
			MessageThreadID: topicID,
			Text:            plain,
			ReplyMarkup:     keyboard,
		})
		return err
	})
	return sent, err
}

// isFormatError reports errors caused by the message content, which another
// format may avoid
func isFormatError(err error) bool {
	if errors.Is(err, bot.ErrorNotFound) {
		return true
	}
	if !errors.Is(err, bot.ErrorBadRequest) || isThreadNotFound(err) {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, s := range []string{"chat not found", "not enough rights", "have no rights", "bot was kicked"} {
		if strings.Contains(text, s) {
			return false
		}
	}
	return true
}

func isThreadNotFound(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "thread not found") || strings.Contains(text, "topic_deleted") ||
		strings.Contains(text, "topic_closed")
}

// isPermanentError reports Telegram errors that retrying cannot fix
func isPermanentError(err error) bool {
	return errors.Is(err, bot.ErrorForbidden) || errors.Is(err, bot.ErrorBadRequest) ||
		errors.Is(err, bot.ErrorUnauthorized)
}

// onEmailError handles a persistent email error
func (b *Bot) onEmailError(accountID int64, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	account, errDB := b.db.GetAccountByID(ctx, accountID)
	if errDB != nil {
		b.logger.Error("failed to get account for error notification", "error", errDB)
		return
	}

	var text string
	if errors.Is(err, email.ErrDecrypt) {
		text = fmt.Sprintf("Не удалось расшифровать пароль почты <b>%s</b>: на сервере сменили ENCRYPTION_KEY.\n\nПереподключите почту: /disconnect, затем /connect", escape(account.Email))
	} else {
		text = fmt.Sprintf("Ошибка подключения к почте <b>%s</b>:\n<code>%s</code>\n\nБот продолжает попытки переподключения. Если сменился пароль — переподключите почту: /disconnect, затем /connect",
			escape(account.Email), escape(err.Error()))
	}
	b.sendMessage(ctx, account.ChatID, account.TopicID, text)
}

// onEmailRecovered notifies that the connection works again
func (b *Bot) onEmailRecovered(accountID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	account, err := b.db.GetAccountByID(ctx, accountID)
	if err != nil {
		b.logger.Error("failed to get account for recovery notification", "error", err)
		return
	}

	b.sendMessage(ctx, account.ChatID, account.TopicID,
		fmt.Sprintf("Подключение к почте <b>%s</b> восстановлено", escape(account.Email)))
}

// onSync persists the UID baseline of an account
func (b *Bot) onSync(accountID int64, uid, uidValidity uint32) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := b.db.UpdateAccountSync(ctx, accountID, uid, uidValidity); err != nil {
		b.logger.Error("failed to update sync state", "error", err, "account_id", accountID)
	}
}
