package email

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/client"

	"github.com/mixelka/emailresend/internal/config"
	"github.com/mixelka/emailresend/pkg/models"
)

const (
	// Messages fetched per batch
	fetchBatchSize = 20
	// Notify the chat after this many failed connection attempts in a row
	notifyAfterFailures = 3
	maxBackoff          = 5 * time.Minute
	commandTimeout      = 30 * time.Second
)

// minBackoff is the delay before reconnecting (variable for tests)
var minBackoff = 5 * time.Second

// ErrNotConnected is returned when an account has no active connection
var ErrNotConnected = errors.New("нет подключения к почтовому серверу")

// MessageHandler handles new email messages
type MessageHandler func(accountID int64, msg *RawEmail)

// ErrorHandler handles persistent connection errors
type ErrorHandler func(accountID int64, err error)

// RecoverHandler is called when a connection is restored after an error was reported
type RecoverHandler func(accountID int64)

// LastUIDHandler persists the last processed UID
type LastUIDHandler func(accountID int64, uid uint32)

// Status of an account connection
const (
	StatusDisconnected = "disconnected"
	StatusConnected    = "connected"
	StatusReconnecting = "reconnecting"
)

// Manager manages all email connections
type Manager struct {
	clients     map[int64]*clientWrapper
	mu          sync.RWMutex
	config      *config.Config
	logger      *slog.Logger
	onMessage   MessageHandler
	onError     ErrorHandler
	onRecover   RecoverHandler
	onLastUID   LastUIDHandler
	decryptFunc func(string) string
	tlsConfig   *tls.Config // overridden in tests
}

type clientWrapper struct {
	account   *models.EmailAccount
	cmds      chan command
	cancel    context.CancelFunc
	done      chan struct{}
	connected atomic.Bool
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

// SetRecoverHandler sets the handler for restored connections
func (m *Manager) SetRecoverHandler(handler RecoverHandler) {
	m.onRecover = handler
}

// SetLastUIDHandler sets the handler that persists the initial UID baseline
func (m *Manager) SetLastUIDHandler(handler LastUIDHandler) {
	m.onLastUID = handler
}

// SetDecryptFunc sets the password decryption function
func (m *Manager) SetDecryptFunc(fn func(string) string) {
	m.decryptFunc = fn
}

func (m *Manager) clientConfig(email, password, server string) ClientConfig {
	return ClientConfig{
		Email:        email,
		Password:     password,
		Server:       server,
		IdleTimeout:  m.config.IMAPIdleTimeout,
		DialTimeout:  m.config.IMAPDialTimeout,
		PollInterval: m.config.EmailPollInterval,
		TLSConfig:    m.tlsConfig,
	}
}

// TestConnection tests an IMAP connection
func (m *Manager) TestConnection(ctx context.Context, email, password, server string) error {
	c := NewClient(m.clientConfig(email, password, server), m.logger)
	defer c.Close()

	_, err := c.Connect(ctx)
	return err
}

// AddAccount starts an email connection. Connecting happens in the background
// and is retried until the account is removed.
func (m *Manager) AddAccount(ctx context.Context, account *models.EmailAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.clients[account.ID]; exists {
		return nil
	}

	clientCtx, cancel := context.WithCancel(context.Background())
	wrapper := &clientWrapper{
		account: account,
		cmds:    make(chan command),
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	m.clients[account.ID] = wrapper

	go m.runClient(clientCtx, wrapper)

	m.logger.Info("added email account", "email", account.Email, "account_id", account.ID)
	return nil
}

// runClient keeps the connection alive and forwards new messages
func (m *Manager) runClient(ctx context.Context, w *clientWrapper) {
	defer close(w.done)

	account := w.account
	logger := m.logger.With("email", account.Email, "account_id", account.ID)

	password := account.Password
	if m.decryptFunc != nil {
		password = m.decryptFunc(password)
	}

	c := NewClient(m.clientConfig(account.Email, password, account.IMAPServer), m.logger)
	defer c.Close()

	lastUID := account.LastUID
	failures := 0
	notified := false
	backoff := minBackoff

	for ctx.Err() == nil {
		// Connect
		mbox, err := c.Connect(ctx)
		if err != nil {
			failures++
			logger.Warn("IMAP connection failed", "error", err, "attempt", failures, "retry_in", backoff)
			if failures == notifyAfterFailures && m.onError != nil {
				m.onError(account.ID, err)
				notified = true
			}
			if !m.sleep(ctx, w.cmds, backoff) {
				return
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		w.connected.Store(true)
		if notified && m.onRecover != nil {
			m.onRecover(account.ID)
		}
		failures, notified, backoff = 0, false, minBackoff

		// First connection of a new account: skip existing mail
		if lastUID == 0 {
			if uid, err := c.HighestUID(mbox); err != nil {
				logger.Warn("failed to get highest UID", "error", err)
			} else if uid > 0 {
				lastUID = uid
				if m.onLastUID != nil {
					m.onLastUID(account.ID, uid)
				}
				logger.Info("initial UID baseline set", "uid", uid)
			}
		}

		// Fetch / wait loop while the connection is alive
		err = m.serve(ctx, c, w, &lastUID)
		w.connected.Store(false)
		c.Close()
		if ctx.Err() != nil {
			return
		}
		logger.Warn("IMAP connection lost, reconnecting", "error", err)
		if !m.sleep(ctx, w.cmds, minBackoff) {
			return
		}
	}
}

// serve fetches new mail and waits for changes until an error occurs
func (m *Manager) serve(ctx context.Context, c *Client, w *clientWrapper, lastUID *uint32) error {
	for {
		for {
			emails, err := c.FetchSince(*lastUID, fetchBatchSize)
			if err != nil {
				return err
			}
			for _, msg := range emails {
				if m.onMessage != nil {
					m.onMessage(w.account.ID, msg)
				}
				if msg.UID > *lastUID {
					*lastUID = msg.UID
				}
			}
			if len(emails) < fetchBatchSize {
				break
			}
		}

		if err := c.WaitForChanges(ctx, w.cmds); err != nil {
			return err
		}
	}
}

// sleep waits for d, rejecting commands meanwhile. Returns false if ctx is done.
func (m *Manager) sleep(ctx context.Context, cmds <-chan command, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		case cmd := <-cmds:
			cmd.done <- ErrNotConnected
		}
	}
}

// exec runs fn on the account's IMAP connection
func (m *Manager) exec(accountID int64, fn func(*client.Client) error) error {
	m.mu.RLock()
	w, exists := m.clients[accountID]
	m.mu.RUnlock()

	if !exists {
		return ErrNotConnected
	}

	timeout := time.NewTimer(commandTimeout)
	defer timeout.Stop()

	cmd := command{fn: fn, done: make(chan error, 1)}
	select {
	case w.cmds <- cmd:
	case <-w.done:
		return ErrNotConnected
	case <-timeout.C:
		return errors.New("почтовый сервер не отвечает")
	}

	select {
	case err := <-cmd.done:
		return err
	case <-timeout.C:
		return errors.New("почтовый сервер не отвечает")
	}
}

// RemoveAccount stops and removes an email connection
func (m *Manager) RemoveAccount(accountID int64) error {
	m.mu.Lock()
	wrapper, exists := m.clients[accountID]
	delete(m.clients, accountID)
	m.mu.Unlock()

	if !exists {
		return nil
	}

	wrapper.cancel()
	m.logger.Info("removed email account", "account_id", accountID)
	return nil
}

// GetStatus returns the status of an account
func (m *Manager) GetStatus(accountID int64) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	wrapper, exists := m.clients[accountID]
	if !exists {
		return StatusDisconnected
	}
	if wrapper.connected.Load() {
		return StatusConnected
	}
	return StatusReconnecting
}

// MarkAsRead marks a message as read
func (m *Manager) MarkAsRead(accountID int64, uid uint32) error {
	return m.exec(accountID, func(c *client.Client) error {
		return markAsRead(c, uid)
	})
}

// DeleteMessage deletes a message
func (m *Manager) DeleteMessage(accountID int64, uid uint32) error {
	return m.exec(accountID, func(c *client.Client) error {
		return deleteMessage(c, uid)
	})
}

// RestoreAll restores all email connections from database
func (m *Manager) RestoreAll(ctx context.Context, accounts []*models.EmailAccount) {
	m.logger.Info("restoring email accounts", "count", len(accounts))
	for _, account := range accounts {
		if err := m.AddAccount(ctx, account); err != nil {
			m.logger.Error("failed to restore account", "email", account.Email, "error", err)
		}
	}
}

// StopAll stops all email connections and waits for them to log out
func (m *Manager) StopAll() {
	m.mu.Lock()
	wrappers := make([]*clientWrapper, 0, len(m.clients))
	for id, wrapper := range m.clients {
		wrapper.cancel()
		wrappers = append(wrappers, wrapper)
		delete(m.clients, id)
	}
	m.mu.Unlock()

	m.logger.Info("stopping all email clients", "count", len(wrappers))

	deadline := time.After(10 * time.Second)
	for _, wrapper := range wrappers {
		select {
		case <-wrapper.done:
		case <-deadline:
			m.logger.Warn("timeout waiting for email clients to stop")
			return
		}
	}
	m.logger.Info("all email clients stopped")
}
