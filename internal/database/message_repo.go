package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mixelka/emailresend/pkg/models"
)

// CreateMessage creates a new email message (ignores if already exists)
func (db *DB) CreateMessage(ctx context.Context, msg *models.EmailMessage) error {
	query := `
		INSERT OR IGNORE INTO email_messages (account_id, uid, message_id, from_addr, from_name, subject, body_text, body_html, received_at, is_read, is_deleted, telegram_msg_id, detected_codes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	now := time.Now()
	result, err := db.ExecContext(ctx, query,
		msg.AccountID,
		msg.UID,
		msg.MessageID,
		msg.FromAddr,
		msg.FromName,
		msg.Subject,
		msg.BodyText,
		msg.BodyHTML,
		msg.ReceivedAt,
		msg.IsRead,
		msg.IsDeleted,
		msg.TelegramMsgID,
		msg.DetectedCodes,
		now,
	)
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	// Check if row was actually inserted (not ignored due to duplicate)
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrAlreadyExists
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	msg.ID = id
	msg.CreatedAt = now
	return nil
}

// GetMessageByID returns a message by ID
func (db *DB) GetMessageByID(ctx context.Context, id int64) (*models.EmailMessage, error) {
	var msg models.EmailMessage
	query := `SELECT * FROM email_messages WHERE id = ?`
	err := db.GetContext(ctx, &msg, query, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}
	return &msg, nil
}

// GetMessageByTelegramMsgID returns a message by Telegram message ID
func (db *DB) GetMessageByTelegramMsgID(ctx context.Context, chatID int64, tgMsgID int) (*models.EmailMessage, error) {
	var msg models.EmailMessage
	query := `
		SELECT m.* FROM email_messages m
		JOIN email_accounts a ON m.account_id = a.id
		WHERE a.chat_id = ? AND m.telegram_msg_id = ?
	`
	err := db.GetContext(ctx, &msg, query, chatID, tgMsgID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}
	return &msg, nil
}

// UpdateMessageTelegramMsgID updates the Telegram message ID
func (db *DB) UpdateMessageTelegramMsgID(ctx context.Context, id int64, tgMsgID int) error {
	query := `UPDATE email_messages SET telegram_msg_id = ? WHERE id = ?`
	_, err := db.ExecContext(ctx, query, tgMsgID, id)
	if err != nil {
		return fmt.Errorf("failed to update telegram msg id: %w", err)
	}
	return nil
}

// MarkMessageAsRead marks a message as read
func (db *DB) MarkMessageAsRead(ctx context.Context, id int64) error {
	query := `UPDATE email_messages SET is_read = true WHERE id = ?`
	_, err := db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to mark message as read: %w", err)
	}
	return nil
}

// MarkMessageAsDeleted marks a message as deleted
func (db *DB) MarkMessageAsDeleted(ctx context.Context, id int64) error {
	query := `UPDATE email_messages SET is_deleted = true WHERE id = ?`
	_, err := db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to mark message as deleted: %w", err)
	}
	return nil
}
