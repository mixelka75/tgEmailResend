package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"
)

// RawEmail represents a raw email message from IMAP
type RawEmail struct {
	UID       uint32
	MessageID string
	From      *Address
	Subject   string
	Date      time.Time
	BodyHTML  string
	BodyText  string
}

// Address represents an email address
type Address struct {
	Name    string
	Address string
}

// ClientConfig configuration for IMAP client
type ClientConfig struct {
	Email       string
	Password    string
	Server      string // host:port
	IdleTimeout time.Duration
	DialTimeout time.Duration
}

// Client IMAP client for a single email account
type Client struct {
	config    ClientConfig
	client    *client.Client
	logger    *slog.Logger
	mu        sync.Mutex
	connected bool
	stopCh    chan struct{}
	stopped   bool
}

// NewClient creates a new IMAP client
func NewClient(cfg ClientConfig, logger *slog.Logger) *Client {
	return &Client{
		config: cfg,
		logger: logger.With("email", cfg.Email),
		stopCh: make(chan struct{}),
	}
}

// Connect connects to the IMAP server
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	c.logger.Info("connecting to IMAP server", "server", c.config.Server)

	// Connect with TLS and timeout
	timeout := c.config.DialTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", c.config.Server, nil)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	imapClient, err := client.New(conn)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to create IMAP client: %w", err)
	}

	// Login
	if err := imapClient.Login(c.config.Email, c.config.Password); err != nil {
		imapClient.Logout()
		return fmt.Errorf("failed to login: %w", err)
	}

	c.client = imapClient
	c.connected = true
	c.logger.Info("connected to IMAP server")

	return nil
}

// SelectINBOX selects the INBOX mailbox
func (c *Client) SelectINBOX(ctx context.Context) (*imap.MailboxStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	mbox, err := c.client.Select("INBOX", false)
	if err != nil {
		return nil, fmt.Errorf("failed to select INBOX: %w", err)
	}

	return mbox, nil
}

// FetchNewMessages fetches new messages with UID > sinceUID
func (c *Client) FetchNewMessages(ctx context.Context, sinceUID uint32) ([]*RawEmail, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Create UID sequence set for UIDs > sinceUID
	seqSet := new(imap.SeqSet)
	seqSet.AddRange(sinceUID+1, 0) // 0 means * (all)

	// Fetch items
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid, imap.FetchBody}
	section := &imap.BodySectionName{}
	items = append(items, section.FetchItem())

	messages := make(chan *imap.Message, 100)
	done := make(chan error, 1)

	go func() {
		done <- c.client.UidFetch(seqSet, items, messages)
	}()

	var emails []*RawEmail
	for msg := range messages {
		email, err := c.parseMessage(msg, section)
		if err != nil {
			c.logger.Warn("failed to parse message", "uid", msg.Uid, "error", err)
			continue
		}
		emails = append(emails, email)
	}

	if err := <-done; err != nil {
		return emails, fmt.Errorf("failed to fetch: %w", err)
	}

	return emails, nil
}

// parseMessage parses an IMAP message into RawEmail
func (c *Client) parseMessage(msg *imap.Message, section *imap.BodySectionName) (*RawEmail, error) {
	email := &RawEmail{
		UID: msg.Uid,
	}

	// Parse envelope
	if msg.Envelope != nil {
		email.Subject = msg.Envelope.Subject
		email.Date = msg.Envelope.Date
		email.MessageID = msg.Envelope.MessageId

		if len(msg.Envelope.From) > 0 {
			from := msg.Envelope.From[0]
			email.From = &Address{
				Name:    from.PersonalName,
				Address: from.Address(),
			}
		}
	}

	// Parse body
	bodyReader := msg.GetBody(section)
	if bodyReader != nil {
		mr, err := mail.CreateReader(bodyReader)
		if err != nil {
			c.logger.Warn("failed to create mail reader", "error", err)
		} else {
			// Read parts
			for {
				part, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					c.logger.Warn("failed to read part", "error", err)
					break
				}

				switch h := part.Header.(type) {
				case *mail.InlineHeader:
					ct, _, _ := h.ContentType()
					body, err := io.ReadAll(part.Body)
					if err != nil {
						continue
					}

					if strings.HasPrefix(ct, "text/html") {
						email.BodyHTML = string(body)
					} else if strings.HasPrefix(ct, "text/plain") {
						email.BodyText = string(body)
					}
				}
			}
		}
	}

	// If no from address parsed, set empty
	if email.From == nil {
		email.From = &Address{}
	}

	return email, nil
}

// MarkAsRead marks a message as read (adds \Seen flag)
func (c *Client) MarkAsRead(ctx context.Context, uid uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return fmt.Errorf("not connected")
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uid)

	item := imap.FormatFlagsOp(imap.AddFlags, true)
	flags := []interface{}{imap.SeenFlag}

	if err := c.client.UidStore(seqSet, item, flags, nil); err != nil {
		return fmt.Errorf("failed to mark as read: %w", err)
	}

	return nil
}

// DeleteMessage deletes a message (adds \Deleted flag and expunges)
func (c *Client) DeleteMessage(ctx context.Context, uid uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return fmt.Errorf("not connected")
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uid)

	item := imap.FormatFlagsOp(imap.AddFlags, true)
	flags := []interface{}{imap.DeletedFlag}

	if err := c.client.UidStore(seqSet, item, flags, nil); err != nil {
		return fmt.Errorf("failed to mark as deleted: %w", err)
	}

	// Expunge deleted messages
	if err := c.client.Expunge(nil); err != nil {
		return fmt.Errorf("failed to expunge: %w", err)
	}

	return nil
}

// GetHighestUID returns the highest UID in the mailbox
func (c *Client) GetHighestUID(ctx context.Context) (uint32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return 0, fmt.Errorf("not connected")
	}

	// Search for all messages
	criteria := imap.NewSearchCriteria()
	uids, err := c.client.UidSearch(criteria)
	if err != nil {
		return 0, fmt.Errorf("failed to search: %w", err)
	}

	if len(uids) == 0 {
		return 0, nil
	}

	// Find highest UID
	var highest uint32
	for _, uid := range uids {
		if uid > highest {
			highest = uid
		}
	}

	return highest, nil
}

// StartIDLE starts IDLE mode for real-time notifications
func (c *Client) StartIDLE(ctx context.Context, onNewMail func()) error {
	c.logger.Info("starting IDLE mode")

	for {
		c.logger.Debug("IDLE loop iteration")
		select {
		case <-ctx.Done():
			c.logger.Info("IDLE: context done")
			return ctx.Err()
		case <-c.stopCh:
			c.logger.Info("IDLE: stop signal received")
			return nil
		default:
		}

		// Check if we need to reconnect
		c.mu.Lock()
		if !c.connected || c.client == nil {
			c.mu.Unlock()
			if err := c.Connect(ctx); err != nil {
				c.logger.Error("failed to reconnect", "error", err)
				time.Sleep(10 * time.Second)
				continue
			}
			if _, err := c.SelectINBOX(ctx); err != nil {
				c.logger.Error("failed to select INBOX after reconnect", "error", err)
				time.Sleep(10 * time.Second)
				continue
			}
		}
		c.mu.Unlock()

		// Start IDLE with timeout
		c.mu.Lock()
		if c.client == nil {
			c.mu.Unlock()
			continue
		}

		idleClient := NewIdleClient(c.client, c.logger)
		c.mu.Unlock()

		// Create stop channel for this IDLE session
		stopIdle := make(chan struct{})

		// Run IDLE in goroutine
		idleDone := make(chan error, 1)
		go func() {
			idleDone <- idleClient.IdleWithFallback(stopIdle, c.config.IdleTimeout)
		}()

		// Wait for IDLE to complete, stop signal, or context done
		c.logger.Info("waiting for IDLE response...")
		select {
		case <-ctx.Done():
			c.logger.Info("IDLE: context cancelled")
			close(stopIdle)
			// Don't wait for idleDone - just return
			return ctx.Err()
		case <-c.stopCh:
			c.logger.Info("IDLE: stop channel closed")
			close(stopIdle)
			// Don't wait for idleDone - just return to avoid blocking
			return nil
		case err := <-idleDone:
			c.logger.Info("IDLE returned", "error", err)
			if err != nil {
				c.logger.Warn("IDLE error", "error", err)
				c.handleDisconnect()
				time.Sleep(5 * time.Second)
				continue
			}
		}

		// Notify about potential new mail
		c.logger.Info("calling onNewMail callback")
		onNewMail()
	}
}

// handleDisconnect handles a disconnect event
func (c *Client) handleDisconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.connected = false
	if c.client != nil {
		c.client.Logout()
		c.client = nil
	}
}

// Stop stops the client
func (c *Client) Stop() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.stopped = true
	imapClient := c.client
	c.client = nil
	c.connected = false
	c.mu.Unlock()

	close(c.stopCh)

	// Close connection in goroutine to avoid blocking
	if imapClient != nil {
		go func() {
			// Try logout with timeout, then force close
			done := make(chan struct{})
			go func() {
				imapClient.Logout()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				// Force close if logout takes too long
				imapClient.Terminate()
			}
		}()
	}
}

// IsConnected returns whether the client is connected
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}
