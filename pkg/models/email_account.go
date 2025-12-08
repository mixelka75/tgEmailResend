package models

import "time"

// EmailAccount represents a connected email account
type EmailAccount struct {
	ID         int64     `db:"id"`
	Email      string    `db:"email"`
	Password   string    `db:"password"`    // Encrypted password
	IMAPServer string    `db:"imap_server"` // e.g., imap.gmail.com:993
	ChatID     int64     `db:"chat_id"`     // Telegram supergroup ID
	TopicID    int       `db:"topic_id"`    // Telegram topic (message_thread_id)
	IsActive   bool      `db:"is_active"`   // Is connection active
	LastUID    uint32    `db:"last_uid"`    // Last processed email UID
	CreatedAt  time.Time `db:"created_at"`
	UpdatedAt  time.Time `db:"updated_at"`
	CreatedBy  int64     `db:"created_by"` // Telegram User ID of admin who created
}
