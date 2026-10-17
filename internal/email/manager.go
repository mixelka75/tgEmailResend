package email

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"github.com/mixelka/emailresend/internal/config"
	"github.com/mixelka/emailresend/pkg/models"
)

const (
	// Messages fetched per batch
	fetchBatchSize = 10
	// Notify the chat after this many failed connection attempts in a row
	notifyAfterFailures = 3
	maxBackoff          = 5 * time.Minute
	commandTimeout      = 30 * time.Second
)

// minBackoff is the delay before reconnecting (variable for tests)
var minBackoff = 5 * time.Second

// ErrNotConnected is returned when an account has no active connection
var ErrNotConnected = errors.New("нет подключения к почтовому серверу")

// ErrDecrypt means a stored password cannot be decrypted (ENCRYPTION_KEY changed)
var ErrDecrypt = errors.New("не удалось расшифровать пароль: ENCRYPTION_KEY изменился")

// MessageHandler handles new email messages. A returned error means the
// message was not delivered and must be retried later.
type MessageHandler func(accountID int64, msg *RawEmail) error

// ErrorHandler handles persistent connection errors
type ErrorHandler func(accountID int64, err error)

// RecoverHandler is called when a connection is restored after an error was reported
type RecoverHandler func(accountID int64)

// SyncHandler persists the UID baseline and UIDVALIDITY of an account
type SyncHandler func(accountID int64, lastUID, uidValidity uint32)

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
	onSync      SyncHandler
	decryptFunc func(string) (string, error)
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

// SetSyncHandler sets the handler that persists the UID baseline
func (m *Manager) SetSyncHandler(handler SyncHandler) {
	m.onSync = handler
}

// SetDecryptFunc sets the password decryption function
func (m *Manager) SetDecryptFunc(fn func(string) (string, error)) {
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

// runClient keeps the connection alive and forwards new messages.
// A panic restarts the loop instead of crashing the whole bot.
func (m *Manager) runClient(ctx context.Context, w *clientWrapper) {
	defer close(w.done)

	for ctx.Err() == nil {
		if m.runClientOnce(ctx, w) {
			return
		}
		// Recovered from a panic
		if !m.sleep(ctx, w.cmds, time.Minute) {
			return
		}
	}
}

// runClientOnce returns true when the account must stop, false after a panic
func (m *Manager) runClientOnce(ctx context.Context, w *clientWrapper) (stop bool) {
	account := w.account
	logger := m.logger.With("email", account.Email, "account_id", account.ID)

	defer func() {
		if r := recover(); r != nil {
			logger.Error("panic in email client, restarting", "panic", r, "stack", string(debug.Stack()))
			w.connected.Store(false)
			stop = false
		}
	}()

	password := account.Password
	if m.decryptFunc != nil {
		var err error
		password, err = m.decryptFunc(password)
		if err != nil {
			// Retrying cannot help: ENCRYPTION_KEY was changed
			logger.Error("failed to decrypt password", "error", err)
			if m.onError != nil {
				m.onError(account.ID, ErrDecrypt)
			}
			<-ctx.Done()
			return true
		}
	}

	c := NewClient(m.clientConfig(account.Email, password, account.IMAPServer), m.logger)
	defer c.Close()

	failures := 0
	notified := false
	backoff := minBackoff

	for ctx.Err() == nil {
		// Connect
		mbox, err := c.Connect(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return true
			}
			failures++
			logger.Warn("IMAP connection failed", "error", err, "attempt", failures, "retry_in", backoff)
			if failures == notifyAfterFailures && m.onError != nil {
				m.onError(account.ID, err)
				notified = true
			}
			if !m.sleep(ctx, w.cmds, backoff) {
				return true
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		w.connected.Store(true)
		if notified && m.onRecover != nil {
			m.onRecover(account.ID)
		}
		failures, notified, backoff = 0, false, minBackoff

		m.syncBaseline(c, mbox, account, logger)

		// Fetch / wait loop while the connection is alive
		err = m.serve(ctx, c, w)
		w.connected.Store(false)
		c.Close()
		if ctx.Err() != nil {
			return true
		}
		logger.Warn("IMAP connection lost, reconnecting", "error", err)
		if !m.sleep(ctx, w.cmds, minBackoff) {
			return true
		}
	}
	return true
}

// syncBaseline sets the starting UID for a new account and after the server
// renumbered the mailbox (UIDVALIDITY changed). Existing mail is skipped.
func (m *Manager) syncBaseline(c *Client, mbox *imap.MailboxStatus, account *models.EmailAccount, logger *slog.Logger) {
	validity := uint32(0)
	if mbox != nil {
		validity = mbox.UidValidity
	}

	reset := ""
	switch {
	case account.LastUID == 0:
		reset = "new account"
	case account.UIDValidity != 0 && validity != 0 && validity != account.UIDValidity:
		reset = "UIDVALIDITY changed"
	case account.UIDValidity == 0 && mbox != nil && mbox.UidNext > 0 && mbox.UidNext-1 < account.LastUID:
		reset = "UIDs went backwards"
	}

	if reset == "" {
		if account.UIDValidity == 0 && validity != 0 {
			account.UIDValidity = validity
			m.persistSync(account)
		}
		return
	}

	uid, err := c.HighestUID(mbox)
	if err != nil {
		logger.Warn("failed to get highest UID", "error", err)
		return
	}
	account.LastUID = uid
	account.UIDValidity = validity
	m.persistSync(account)
	logger.Info("UID baseline set", "reason", reset, "uid", uid, "uid_validity", validity)
}

func (m *Manager) persistSync(account *models.EmailAccount) {
	if m.onSync != nil {
		m.onSync(account.ID, account.LastUID, account.UIDValidity)
	}
}

// serve fetches new mail and waits for changes until an error occurs
func (m *Manager) serve(ctx context.Context, c *Client, w *clientWrapper) error {
	account := w.account
	for {
	batches:
		for {
			emails, err := c.FetchSince(account.LastUID, fetchBatchSize)
			if err != nil {
				return err
			}
			for _, msg := range emails {
				if m.onMessage != nil {
					if err := m.onMessage(account.ID, msg); err != nil {
						// Telegram is unavailable: keep the UID, retry on the next check
						m.logger.Warn("failed to deliver message, will retry",
							"account_id", account.ID, "uid", msg.UID, "error", err)
						break batches
					}
				}
				if msg.UID > account.LastUID {
					account.LastUID = msg.UID
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
