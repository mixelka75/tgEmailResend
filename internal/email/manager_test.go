package email

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
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
	m.SetMessageHandler(func(_ int64, msg *RawEmail) { received <- msg })
	m.SetLastUIDHandler(func(_ int64, uid uint32) { baseline <- uid })

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

	if err := m.DeleteMessage(1, 8); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if m.GetStatus(1) != StatusConnected {
		t.Errorf("status = %s, want connected", m.GetStatus(1))
	}

	m.RemoveAccount(1)
	if err := m.MarkAsRead(1, 7); err != ErrNotConnected {
		t.Errorf("MarkAsRead after remove = %v, want ErrNotConnected", err)
	}
}
