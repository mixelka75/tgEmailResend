package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mixelka/emailresend/pkg/models"
)

// ErrNotFound is returned when a record is not found
var ErrNotFound = errors.New("record not found")

// ErrAlreadyExists is returned when trying to insert a duplicate record
var ErrAlreadyExists = errors.New("record already exists")

// CreateAccount creates a new email account
func (db *DB) CreateAccount(ctx context.Context, account *models.EmailAccount) error {
	query := `
		INSERT INTO email_accounts (email, password, imap_server, chat_id, topic_id, is_active, last_uid, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	now := time.Now()
	result, err := db.ExecContext(ctx, query,
		account.Email,
		account.Password,
		account.IMAPServer,
		account.ChatID,
		account.TopicID,
		account.IsActive,
		account.LastUID,
		account.CreatedBy,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("failed to create account: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	account.ID = id
	account.CreatedAt = now
	account.UpdatedAt = now
	return nil
}

// GetAccountByID returns an account by ID
func (db *DB) GetAccountByID(ctx context.Context, id int64) (*models.EmailAccount, error) {
	var account models.EmailAccount
	query := `SELECT * FROM email_accounts WHERE id = ?`
	err := db.GetContext(ctx, &account, query, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}
	return &account, nil
}

// GetAccountByChatAndTopic returns an account by chat ID and topic ID
func (db *DB) GetAccountByChatAndTopic(ctx context.Context, chatID int64, topicID int) (*models.EmailAccount, error) {
	var account models.EmailAccount
	query := `SELECT * FROM email_accounts WHERE chat_id = ? AND topic_id = ?`
	err := db.GetContext(ctx, &account, query, chatID, topicID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}
	return &account, nil
}

// GetAccountsByChatID returns all accounts for a chat
func (db *DB) GetAccountsByChatID(ctx context.Context, chatID int64) ([]*models.EmailAccount, error) {
	var accounts []*models.EmailAccount
	query := `SELECT * FROM email_accounts WHERE chat_id = ? ORDER BY created_at DESC`
	err := db.SelectContext(ctx, &accounts, query, chatID)
	if err != nil {
		return nil, fmt.Errorf("failed to get accounts: %w", err)
	}
	return accounts, nil
}

// GetAllActiveAccounts returns all active accounts
func (db *DB) GetAllActiveAccounts(ctx context.Context) ([]*models.EmailAccount, error) {
	var accounts []*models.EmailAccount
	query := `SELECT * FROM email_accounts WHERE is_active = true`
	err := db.SelectContext(ctx, &accounts, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get active accounts: %w", err)
	}
	return accounts, nil
}

// UpdateAccountLastUID updates the last processed UID
func (db *DB) UpdateAccountLastUID(ctx context.Context, id int64, uid uint32) error {
	query := `UPDATE email_accounts SET last_uid = ?, updated_at = ? WHERE id = ?`
	_, err := db.ExecContext(ctx, query, uid, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update last uid: %w", err)
	}
	return nil
}

// SetAccountActive sets the active status of an account
func (db *DB) SetAccountActive(ctx context.Context, id int64, active bool) error {
	query := `UPDATE email_accounts SET is_active = ?, updated_at = ? WHERE id = ?`
	_, err := db.ExecContext(ctx, query, active, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to set account active: %w", err)
	}
	return nil
}

// DeleteAccount deletes an account
func (db *DB) DeleteAccount(ctx context.Context, id int64) error {
	query := `DELETE FROM email_accounts WHERE id = ?`
	_, err := db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete account: %w", err)
	}
	return nil
}
