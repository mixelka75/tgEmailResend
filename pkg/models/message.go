package models

import "time"

// EmailMessage represents an email message
type EmailMessage struct {
	ID            int64     `db:"id"`
	AccountID     int64     `db:"account_id"`      // FK to EmailAccount
	UID           uint32    `db:"uid"`             // IMAP UID
	MessageID     string    `db:"message_id"`      // Email Message-ID header
	FromAddr      string    `db:"from_addr"`       // Sender email
	FromName      string    `db:"from_name"`       // Sender name
	Subject       string    `db:"subject"`         // Email subject
	BodyText      string    `db:"body_text"`       // Parsed text body
	BodyHTML      string    `db:"body_html"`       // Original HTML body
	ReceivedAt    time.Time `db:"received_at"`     // When email was received
	IsRead        bool      `db:"is_read"`         // Marked as read
	IsDeleted     bool      `db:"is_deleted"`      // Marked as deleted
	TelegramMsgID int       `db:"telegram_msg_id"` // Telegram message ID
	DetectedCodes string    `db:"detected_codes"`  // JSON array of detected codes
	CreatedAt     time.Time `db:"created_at"`
}

// DetectedCode represents a detected verification code
type DetectedCode struct {
	Type  string `json:"type"`  // "otp", "verification", "pin", "code"
	Value string `json:"value"` // The code itself
}
