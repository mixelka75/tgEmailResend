package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
)

func init() {
	// Decode non-UTF-8 headers and bodies (windows-1251, koi8-r, ...)
	imap.CharsetReader = charset.Reader
	message.CharsetReader = charset.Reader
}

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
	Email        string
	Password     string
	Server       string // host:port
	IdleTimeout  time.Duration
	DialTimeout  time.Duration
	PollInterval time.Duration // max time between mailbox checks
	TLSConfig    *tls.Config   // nil means default verification against Server
}

// command is an IMAP operation requested from outside the connection goroutine
type command struct {
	fn   func(*client.Client) error
	done chan error
}

// Client is a single IMAP connection. It is not safe for concurrent use:
// it is owned by one goroutine, other goroutines send it commands.
type Client struct {
	config  ClientConfig
	logger  *slog.Logger
	client  *client.Client
	newMail chan struct{}
}

// NewClient creates a new IMAP client
func NewClient(cfg ClientConfig, logger *slog.Logger) *Client {
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 30 * time.Second
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = time.Minute
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 25 * time.Minute
	}
	return &Client{
		config: cfg,
		logger: logger.With("email", cfg.Email),
	}
}

// Connect connects, logs in and selects INBOX
func (c *Client) Connect(ctx context.Context) (*imap.MailboxStatus, error) {
	c.Close()

	c.logger.Debug("connecting to IMAP server", "server", c.config.Server)

	tlsConfig := c.config.TLSConfig
	if tlsConfig == nil {
		host, _, _ := net.SplitHostPort(c.config.Server)
		tlsConfig = &tls.Config{ServerName: host}
	}
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: c.config.DialTimeout, KeepAlive: 30 * time.Second},
		Config:    tlsConfig,
	}
	conn, err := dialer.DialContext(ctx, "tcp", c.config.Server)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	imapClient, err := client.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create IMAP client: %w", err)
	}

	// Every command (IDLE included) must finish within this time,
	// otherwise the connection is considered dead
	imapClient.Timeout = c.config.PollInterval + 2*time.Minute

	// Unilateral server updates (new mail during IDLE). The channel must be
	// drained all the time, otherwise the IMAP reader blocks.
	updates := make(chan client.Update, 64)
	newMail := make(chan struct{}, 1)
	imapClient.Updates = updates
	go func() {
		for {
			select {
			case u := <-updates:
				if _, ok := u.(*client.MailboxUpdate); ok {
					select {
					case newMail <- struct{}{}:
					default:
					}
				}
			case <-imapClient.LoggedOut():
				return
			}
		}
	}()

	if err := imapClient.Login(c.config.Email, c.config.Password); err != nil {
		imapClient.Terminate()
		return nil, fmt.Errorf("failed to login: %w", err)
	}

	mbox, err := imapClient.Select("INBOX", false)
	if err != nil {
		imapClient.Terminate()
		return nil, fmt.Errorf("failed to select INBOX: %w", err)
	}

	c.client = imapClient
	c.newMail = newMail
	c.logger.Info("connected to IMAP server", "server", c.config.Server)
	return mbox, nil
}

// Close logs out and closes the connection
func (c *Client) Close() {
	if c.client == nil {
		return
	}
	imapClient := c.client
	c.client, c.newMail = nil, nil

	done := make(chan struct{})
	go func() {
		imapClient.Logout()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		imapClient.Terminate()
	}
}

// HighestUID returns the UID of the newest message in the selected mailbox
func (c *Client) HighestUID(mbox *imap.MailboxStatus) (uint32, error) {
	if c.client == nil {
		return 0, errors.New("not connected")
	}
	if mbox != nil && mbox.UidNext > 0 {
		return mbox.UidNext - 1, nil
	}

	uids, err := c.client.UidSearch(imap.NewSearchCriteria())
	if err != nil {
		return 0, fmt.Errorf("failed to search: %w", err)
	}
	var highest uint32
	for _, uid := range uids {
		if uid > highest {
			highest = uid
		}
	}
	return highest, nil
}

// FetchSince fetches up to limit messages with UID > sinceUID, oldest first
func (c *Client) FetchSince(sinceUID uint32, limit int) ([]*RawEmail, error) {
	if c.client == nil {
		return nil, errors.New("not connected")
	}

	// "N:*" always matches at least the last message, even if its UID < N,
	// so filter the result
	criteria := imap.NewSearchCriteria()
	criteria.Uid = new(imap.SeqSet)
	criteria.Uid.AddRange(sinceUID+1, 0)

	found, err := c.client.UidSearch(criteria)
	if err != nil {
		return nil, fmt.Errorf("failed to search: %w", err)
	}

	var uids []uint32
	for _, uid := range found {
		if uid > sinceUID {
			uids = append(uids, uid)
		}
	}
	if len(uids) == 0 {
		return nil, nil
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	if len(uids) > limit {
		uids = uids[:limit]
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uids...)

	// PEEK so that forwarding does not mark mail as read on the server
	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid, section.FetchItem()}

	messages := make(chan *imap.Message, len(uids))
	done := make(chan error, 1)
	go func() {
		done <- c.client.UidFetch(seqSet, items, messages)
	}()

	var emails []*RawEmail
	for msg := range messages {
		emails = append(emails, c.parseMessage(msg, section))
	}
	if err := <-done; err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}

	sort.Slice(emails, func(i, j int) bool { return emails[i].UID < emails[j].UID })
	return emails, nil
}

// parseMessage parses an IMAP message into RawEmail
func (c *Client) parseMessage(msg *imap.Message, section *imap.BodySectionName) *RawEmail {
	email := &RawEmail{
		UID:  msg.Uid,
		From: &Address{},
	}

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

	bodyReader := msg.GetBody(section)
	if bodyReader == nil {
		return email
	}

	mr, err := mail.CreateReader(bodyReader)
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		c.logger.Warn("failed to create mail reader", "uid", msg.Uid, "error", err)
		return email
	}
	if mr == nil {
		return email
	}

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
			c.logger.Warn("failed to read part", "uid", msg.Uid, "error", err)
			break
		}
		if part == nil {
			continue
		}

		h, ok := part.Header.(*mail.InlineHeader)
		if !ok {
			continue
		}
		ct, _, _ := h.ContentType()
		body, err := io.ReadAll(part.Body)
		if err != nil {
			continue
		}

		// Keep the first part of each type (later ones are usually forwarded/quoted parts)
		switch {
		case strings.HasPrefix(ct, "text/html") && email.BodyHTML == "":
			email.BodyHTML = string(body)
		case (ct == "" || strings.HasPrefix(ct, "text/plain")) && email.BodyText == "":
			email.BodyText = string(body)
		}
	}

	return email
}

// WaitForChanges blocks until new mail may have arrived, maxWait elapsed,
// or ctx is done. Commands received meanwhile are executed on this connection.
func (c *Client) WaitForChanges(ctx context.Context, cmds <-chan command) error {
	if c.client == nil {
		return errors.New("not connected")
	}

	timer := time.NewTimer(c.config.PollInterval)
	defer timer.Stop()

	for {
		stop := make(chan struct{})
		idleDone := make(chan error, 1)
		go func() {
			idleDone <- c.client.Idle(stop, &client.IdleOptions{
				LogoutTimeout: c.config.IdleTimeout,
				PollInterval:  c.config.PollInterval,
			})
		}()

		var cmd *command
		var wakeErr error
		finished := false

		select {
		case <-ctx.Done():
			wakeErr = ctx.Err()
		case <-c.newMail:
			finished = true
		case <-timer.C:
			finished = true
		case received := <-cmds:
			cmd = &received
		case err := <-idleDone:
			// IDLE ended on its own: the connection is broken
			if err == nil {
				err = errors.New("IDLE ended unexpectedly")
			}
			return err
		}

		close(stop)
		select {
		case err := <-idleDone:
			if err != nil {
				if cmd != nil {
					cmd.done <- err
				}
				return fmt.Errorf("IDLE failed: %w", err)
			}
		case <-time.After(30 * time.Second):
			if cmd != nil {
				cmd.done <- errors.New("IDLE did not stop")
			}
			return errors.New("IDLE did not stop in time")
		}

		if wakeErr != nil {
			return wakeErr
		}
		if cmd != nil {
			cmd.done <- cmd.fn(c.client)
			continue
		}
		if finished {
			return nil
		}
	}
}

// markAsRead adds the \Seen flag
func markAsRead(cl *client.Client, uid uint32) error {
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uid)

	item := imap.FormatFlagsOp(imap.AddFlags, true)
	if err := cl.UidStore(seqSet, item, []interface{}{imap.SeenFlag}, nil); err != nil {
		return fmt.Errorf("failed to mark as read: %w", err)
	}
	return nil
}

// deleteMessage adds the \Deleted flag and expunges
func deleteMessage(cl *client.Client, uid uint32) error {
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uid)

	item := imap.FormatFlagsOp(imap.AddFlags, true)
	if err := cl.UidStore(seqSet, item, []interface{}{imap.DeletedFlag}, nil); err != nil {
		return fmt.Errorf("failed to mark as deleted: %w", err)
	}
	if err := cl.Expunge(nil); err != nil {
		return fmt.Errorf("failed to expunge: %w", err)
	}
	return nil
}
