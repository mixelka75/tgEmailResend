package email

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mixelka/emailresend/internal/config"
	"github.com/mixelka/emailresend/pkg/models"
)

// MessageHandler handles new email messages
type MessageHandler func(accountID int64, msg *RawEmail)

// ErrorHandler handles email errors
type ErrorHandler func(accountID int64, err error)

// Manager manages all email connections
type Manager struct {
	clients      map[int64]*clientWrapper
	mu           sync.RWMutex
	config       *config.Config
	logger       *slog.Logger
	onMessage    MessageHandler
	onError      ErrorHandler
	decryptFunc  func(string) string
}

type clientWrapper struct {
	client    *Client
	account   *models.EmailAccount
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewManager creates a new email manager
func NewManager(cfg *config.Config, logger *slog.Logger) *Manager {
	return &Manager{
		clients: make(map[int64]*clientWrapper),
		config:  cfg,
		logger:  logger.With("component", "email_manager"),
	}
}

// SetMessageHandler sets the handler for new messages
func (m *Manager) SetMessageHandler(handler MessageHandler) {
	m.onMessage = handler
}

// SetErrorHandler sets the handler for errors
func (m *Manager) SetErrorHandler(handler ErrorHandler) {
	m.onError = handler
}

// SetDecryptFunc sets the password decryption function
func (m *Manager) SetDecryptFunc(fn func(string) string) {
	m.decryptFunc = fn
}

// TestConnection tests an IMAP connection
func (m *Manager) TestConnection(ctx context.Context, email, password, server string) error {
	client := NewClient(ClientConfig{
		Email:       email,
		Password:    password,
		Server:      server,
		IdleTimeout: m.config.IMAPIdleTimeout,
		DialTimeout: m.config.IMAPDialTimeout,
	}, m.logger)

	if err := client.Connect(ctx); err != nil {
		return err
	}

	if _, err := client.SelectINBOX(ctx); err != nil {
		client.Stop()
		return err
	}

	client.Stop()
	return nil
}

// AddAccount adds and starts an email connection
func (m *Manager) AddAccount(ctx context.Context, account *models.EmailAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already exists
	if _, exists := m.clients[account.ID]; exists {
		return nil
	}

	// Decrypt password
	password := account.Password
	if m.decryptFunc != nil {
		password = m.decryptFunc(password)
	}

	// Create client
	client := NewClient(ClientConfig{
		Email:       account.Email,
		Password:    password,
		Server:      account.IMAPServer,
		IdleTimeout: m.config.IMAPIdleTimeout,
		DialTimeout: m.config.IMAPDialTimeout,
	}, m.logger)

	// Connect
	if err := client.Connect(ctx); err != nil {
		return err
	}

	// Select INBOX
	if _, err := client.SelectINBOX(ctx); err != nil {
		client.Stop()
		return err
	}

	// Create wrapper
	clientCtx, cancel := context.WithCancel(context.Background())
	wrapper := &clientWrapper{
		client:  client,
		account: account,
		ctx:     clientCtx,
		cancel:  cancel,
	}

	m.clients[account.ID] = wrapper

	// Start IDLE goroutine
	go m.runClient(wrapper)

	m.logger.Info("added email account", "email", account.Email, "account_id", account.ID)
	return nil
}

// runClient runs the email client
func (m *Manager) runClient(wrapper *clientWrapper) {
	lastUID := wrapper.account.LastUID

	// Initial fetch of new messages
	m.fetchNewMessages(wrapper, &lastUID)

	// Start IDLE
	wrapper.client.StartIDLE(wrapper.ctx, func() {
		m.fetchNewMessages(wrapper, &lastUID)
	})
}

// fetchNewMessages fetches and processes new messages
func (m *Manager) fetchNewMessages(wrapper *clientWrapper, lastUID *uint32) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Select INBOX (in case of reconnect)
	if _, err := wrapper.client.SelectINBOX(ctx); err != nil {
		m.logger.Error("failed to select INBOX", "error", err, "account_id", wrapper.account.ID)
		if m.onError != nil {
			m.onError(wrapper.account.ID, err)
		}
		return
	}

	// Fetch new messages
	messages, err := wrapper.client.FetchNewMessages(ctx, *lastUID)
	if err != nil {
		m.logger.Error("failed to fetch messages", "error", err, "account_id", wrapper.account.ID)
		if m.onError != nil {
			m.onError(wrapper.account.ID, err)
		}
		return
	}

	// Process messages
	for _, msg := range messages {
		if m.onMessage != nil {
			m.onMessage(wrapper.account.ID, msg)
		}

		// Update last UID
		if msg.UID > *lastUID {
			*lastUID = msg.UID
		}
	}
}

// RemoveAccount stops and removes an email connection
func (m *Manager) RemoveAccount(accountID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	wrapper, exists := m.clients[accountID]
	if !exists {
		return nil
	}

	wrapper.cancel()
	wrapper.client.Stop()
	delete(m.clients, accountID)

	m.logger.Info("removed email account", "account_id", accountID)
	return nil
}

// GetStatus returns the status of an account
func (m *Manager) GetStatus(accountID int64) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	wrapper, exists := m.clients[accountID]
	if !exists {
		return "disconnected"
	}

	if wrapper.client.IsConnected() {
		return "connected"
	}
	return "reconnecting"
}

// MarkAsRead marks a message as read
func (m *Manager) MarkAsRead(accountID int64, uid uint32) error {
	m.mu.RLock()
	wrapper, exists := m.clients[accountID]
	m.mu.RUnlock()

	if !exists {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return wrapper.client.MarkAsRead(ctx, uid)
}

// DeleteMessage deletes a message
func (m *Manager) DeleteMessage(accountID int64, uid uint32) error {
	m.mu.RLock()
	wrapper, exists := m.clients[accountID]
	m.mu.RUnlock()

	if !exists {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return wrapper.client.DeleteMessage(ctx, uid)
}

// RestoreAll restores all email connections from database
func (m *Manager) RestoreAll(ctx context.Context, accounts []*models.EmailAccount) {
	m.logger.Info("restoring email accounts", "count", len(accounts))

	var wg sync.WaitGroup
	for _, account := range accounts {
		wg.Add(1)
		go func(acc *models.EmailAccount) {
			defer wg.Done()
			if err := m.AddAccount(ctx, acc); err != nil {
				m.logger.Error("failed to restore account", "email", acc.Email, "error", err)
			}
		}(account)
	}
	wg.Wait()

	m.logger.Info("finished restoring email accounts")
}

// StopAll stops all email connections
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.logger.Info("stopping all email clients")

	for id, wrapper := range m.clients {
		wrapper.cancel()
		wrapper.client.Stop()
		delete(m.clients, id)
	}

	m.logger.Info("all email clients stopped")
}
