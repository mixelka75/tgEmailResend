package email

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/server"

	"github.com/mixelka/emailresend/internal/config"
	"github.com/mixelka/emailresend/pkg/models"
)

// trackingListener remembers accepted connections so a test can drop them
type trackingListener struct {
	net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func (l *trackingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.mu.Lock()
		l.conns = append(l.conns, c)
		l.mu.Unlock()
	}
	return c, err
}

func (l *trackingListener) dropAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.conns {
		c.Close()
	}
	l.conns = nil
}

// startIMAPServer starts an in-memory IMAPS server. Its INBOX has one message with UID 6.
func startIMAPServer(t *testing.T) (addr string, be *memory.Backend, ln *trackingListener, clientTLS *tls.Config) {
	t.Helper()

	// Borrow a self-signed certificate for 127.0.0.1 from httptest
	ts := httptest.NewUnstartedServer(nil)
	ts.StartTLS()
	cert := ts.TLS.Certificates[0]
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	ts.Close()

	raw, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	ln = &trackingListener{Listener: raw}

	be = memory.New()
	s := server.New(be)
	s.AllowInsecureAuth = true
	go s.Serve(ln)
	t.Cleanup(func() { s.Close() })

	return raw.Addr().String(), be, ln, &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"}
}

func appendMessage(t *testing.T, be *memory.Backend, subject, body string) {
	t.Helper()
	user, err := be.Login(nil, "username", "password")
	if err != nil {
		t.Fatal(err)
	}
	mbox, err := user.GetMailbox("INBOX")
	if err != nil {
		t.Fatal(err)
	}
	msg := "From: Shop <shop@example.org>\r\nSubject: " + subject + "\r\nContent-Type: text/plain\r\n\r\n" + body
	if err := mbox.CreateMessage(nil, time.Now(), strings.NewReader(msg)); err != nil {
		t.Fatal(err)
	}
}

func messageFlags(t *testing.T, be *memory.Backend, uid uint32) []string {
	t.Helper()
	user, _ := be.Login(nil, "username", "password")
	mbox, _ := user.GetMailbox("INBOX")
	for _, m := range mbox.(*memory.Mailbox).Messages {
		if m.Uid == uid {
			return m.Flags
		}
	}
	return nil
}

func setFlags(t *testing.T, be *memory.Backend, uid uint32, flags ...string) {
	t.Helper()
	user, _ := be.Login(nil, "username", "password")
	mbox, _ := user.GetMailbox("INBOX")
	for _, m := range mbox.(*memory.Mailbox).Messages {
		if m.Uid == uid {
			m.Flags = append(m.Flags, flags...)
		}
	}
}

func hasFlag(flags []string, flag string) bool {
	for _, f := range flags {
		if f == flag {
			return true
		}
	}
	return false
}

func waitFor[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
		var zero T
		return zero
	}
}

func TestManagerEndToEnd(t *testing.T) {
	minBackoff = 100 * time.Millisecond

	addr, be, ln, clientTLS := startIMAPServer(t)

	cfg := &config.Config{
		IMAPIdleTimeout:   time.Minute,
		IMAPDialTimeout:   5 * time.Second,
		EmailPollInterval: 300 * time.Millisecond,
	}
	m := NewManager(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.tlsConfig = clientTLS

	received := make(chan *RawEmail, 10)
	baseline := make(chan uint32, 1)
	var failNext atomic.Bool
	m.SetMessageHandler(func(_ int64, msg *RawEmail) error {
		if failNext.CompareAndSwap(true, false) {
			return errors.New("telegram is down")
		}
		received <- msg
		return nil
	})
	m.SetSyncHandler(func(_ int64, uid, _ uint32) { baseline <- uid })

	if err := m.TestConnection(t.Context(), "username", "password", addr); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if err := m.TestConnection(t.Context(), "username", "wrong", addr); err == nil {
		t.Fatal("TestConnection with wrong password succeeded")
	}

	account := &models.EmailAccount{ID: 1, Email: "username", Password: "password", IMAPServer: addr}
	if err := m.AddAccount(t.Context(), account); err != nil {
		t.Fatal(err)
	}
	defer m.StopAll()

	// Existing mail (UID 6) must not be forwarded, only remembered
	if uid := waitFor(t, baseline, "baseline"); uid != 6 {
		t.Fatalf("baseline UID = %d, want 6", uid)
	}

	// The first delivery attempt fails: the message must be retried, not lost
	failNext.Store(true)
	appendMessage(t, be, "Login", "Код подтверждения: 482913")
	msg := waitFor(t, received, "new message")
	if msg.UID != 7 || msg.Subject != "Login" || !strings.Contains(msg.BodyText, "482913") {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if msg.From.Address != "shop@example.org" {
		t.Errorf("From = %+v", msg.From)
	}

	// Forwarding must not mark mail as read on the server
	for _, f := range messageFlags(t, be, 7) {
		if f == imap.SeenFlag {
			t.Fatal("forwarded message was marked as \\Seen")
		}
	}

	// A command sent while the client is idling
	if err := m.MarkAsRead(1, 7); err != nil {
		t.Fatalf("MarkAsRead: %v", err)
	}
	seen := false
	for _, f := range messageFlags(t, be, 7) {
		seen = seen || f == imap.SeenFlag
	}
	if !seen {
		t.Fatal("MarkAsRead did not set \\Seen")
	}

	// Connection drop: the client must reconnect and keep forwarding
	ln.dropAll()
	appendMessage(t, be, "After reconnect", "hello")
	msg = waitFor(t, received, "message after reconnect")
	if msg.UID != 8 {
		t.Fatalf("after reconnect got UID %d, want 8", msg.UID)
	}

	select {
	case extra := <-received:
		t.Fatalf("message forwarded twice: UID %d", extra.UID)
	case <-time.After(time.Second):
	}

	// Another mail client flagged UID 7 as \Deleted without expunging:
	// deleting UID 8 must not wipe it
	setFlags(t, be, 7, imap.DeletedFlag)
	if err := m.DeleteMessage(1, 8); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if messageFlags(t, be, 7) == nil {
		t.Fatal("DeleteMessage expunged another message")
	}
	if !hasFlag(messageFlags(t, be, 8), imap.DeletedFlag) {
		t.Fatal("deleted message is not flagged \\Deleted")
	}
	if m.GetStatus(1) != StatusConnected {
		t.Errorf("status = %s, want connected", m.GetStatus(1))
	}

	m.RemoveAccount(1)
	if err := m.MarkAsRead(1, 7); err != ErrNotConnected {
		t.Errorf("MarkAsRead after remove = %v, want ErrNotConnected", err)
	}
}

func newTestManager(t *testing.T, clientTLS *tls.Config) *Manager {
	t.Helper()
	minBackoff = 100 * time.Millisecond
	cfg := &config.Config{
		IMAPIdleTimeout:   time.Minute,
		IMAPDialTimeout:   5 * time.Second,
		EmailPollInterval: 300 * time.Millisecond,
	}
	m := NewManager(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.tlsConfig = clientTLS
	return m
}

func TestDeleteMovesToTrash(t *testing.T) {
	addr, be, _, clientTLS := startIMAPServer(t)
	user, _ := be.Login(nil, "username", "password")
	if err := user.CreateMailbox("Trash"); err != nil {
		t.Fatal(err)
	}

	m := newTestManager(t, clientTLS)
	synced := make(chan uint32, 1)
	m.SetMessageHandler(func(int64, *RawEmail) error { return nil })
	m.SetSyncHandler(func(_ int64, uid, _ uint32) { synced <- uid })
	m.AddAccount(context.Background(), &models.EmailAccount{ID: 1, Email: "username", Password: "password", IMAPServer: addr})
	defer m.StopAll()
	waitFor(t, synced, "baseline")

	if err := m.DeleteMessage(1, 6); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	trash, _ := user.GetMailbox("Trash")
	if n := len(trash.(*memory.Mailbox).Messages); n != 1 {
		t.Fatalf("Trash has %d messages, want 1", n)
	}
}

func TestDecryptErrorStopsAccount(t *testing.T) {
	m := newTestManager(t, nil)
	errs := make(chan error, 2)
	m.SetDecryptFunc(func(string) (string, error) { return "", errors.New("bad key") })
	m.SetErrorHandler(func(_ int64, err error) { errs <- err })
	m.AddAccount(context.Background(), &models.EmailAccount{ID: 1, Email: "x", Password: "enc", IMAPServer: "127.0.0.1:1"})
	defer m.StopAll()

	if err := waitFor(t, errs, "decrypt error"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("error = %v, want ErrDecrypt", err)
	}
	select {
	case err := <-errs:
		t.Fatalf("decrypt error reported twice: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestEmptyMailboxKeepsMailArrivedWhileDisconnected(t *testing.T) {
	addr, be, ln, clientTLS := startIMAPServer(t)

	// Empty INBOX: the baseline is UID 0
	user, _ := be.Login(nil, "username", "password")
	inbox, _ := user.GetMailbox("INBOX")
	inbox.(*memory.Mailbox).Messages = nil

	m := newTestManager(t, clientTLS)
	received := make(chan *RawEmail, 4)
	synced := make(chan uint32, 4)
	m.SetMessageHandler(func(_ int64, msg *RawEmail) error { received <- msg; return nil })
	m.SetSyncHandler(func(_ int64, uid, _ uint32) { synced <- uid })
	account := &models.EmailAccount{ID: 1, Email: "username", Password: "password", IMAPServer: addr}
	m.AddAccount(context.Background(), account)
	defer m.StopAll()

	if uid := waitFor(t, synced, "baseline"); uid != 0 {
		t.Fatalf("baseline = %d, want 0", uid)
	}

	// Mail arrives right as the connection drops
	ln.dropAll()
	appendMessage(t, be, "While disconnected", "hi")
	if msg := waitFor(t, received, "message after reconnect"); msg.Subject != "While disconnected" {
		t.Fatalf("got %q", msg.Subject)
	}
}
