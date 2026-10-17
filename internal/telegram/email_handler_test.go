package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mixelka/emailresend/internal/config"
	"github.com/mixelka/emailresend/internal/database"
	"github.com/mixelka/emailresend/internal/email"
	"github.com/mixelka/emailresend/internal/formatter"
	"github.com/mixelka/emailresend/internal/parser"
	"github.com/mixelka/emailresend/pkg/models"
)

// fakeTelegram is a minimal Bot API server
type fakeTelegram struct {
	mu    sync.Mutex
	calls []fakeCall
	// reply decides the answer for a method call; nil means success
	reply func(method string, form map[string]string) (status int, body string)
}

type fakeCall struct {
	method string
	form   map[string]string
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	form := map[string]string{}
	if err := r.ParseMultipartForm(1 << 20); err == nil {
		for k, v := range r.MultipartForm.Value {
			form[k] = v[0]
		}
	}

	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{method, form})
	reply := f.reply
	f.mu.Unlock()

	if reply != nil {
		if status, body := reply(method, form); status != 0 {
			w.WriteHeader(status)
			io.WriteString(w, body)
			return
		}
	}
	switch method {
	case "getMe":
		io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"b","username":"test_bot"}}`)
	default:
		io.WriteString(w, `{"ok":true,"result":{"message_id":777,"date":0,"chat":{"id":-100,"type":"supergroup"}}}`)
	}
}

func (f *fakeTelegram) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if c.method != "getMe" {
			out = append(out, c.method)
		}
	}
	return out
}

func (f *fakeTelegram) last(method string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].method == method {
			return f.calls[i].form
		}
	}
	return nil
}

func newTestBot(t *testing.T, fake *fakeTelegram) (*Bot, *database.DB, *models.EmailAccount) {
	t.Helper()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	db, err := database.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		TelegramToken:        "1:x",
		TelegramAPIURL:       srv.URL,
		TelegramRichMessages: true,
		EncryptionKey:        strings.Repeat("k", 32),
		EmailPollInterval:    time.Minute,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	b, err := NewBot(context.Background(), BotDeps{
		Config:       cfg,
		DB:           db,
		EmailManager: email.NewManager(cfg, logger),
		HTMLParser:   parser.NewHTMLParser(),
		CodeDetector: parser.NewCodeDetector(),
		Formatter:    formatter.NewTelegramFormatter(),
		Logger:       logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	account := &models.EmailAccount{Email: "a@b.c", Password: "x", IMAPServer: "imap.b.c:993", ChatID: -100, TopicID: 5, IsActive: true, CreatedBy: 1}
	if err := db.CreateAccount(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	return b, db, account
}

func testEmail(uid uint32) *email.RawEmail {
	return &email.RawEmail{
		UID:      uid,
		From:     &email.Address{Name: "Shop", Address: "shop@example.com"},
		Subject:  "Вход",
		Date:     time.Now(),
		BodyHTML: "<p>Код подтверждения: <b>482913</b></p>",
	}
}

func TestDeliverRichMessage(t *testing.T) {
	fake := &fakeTelegram{}
	b, db, account := newTestBot(t, fake)

	if err := b.onNewEmail(account.ID, testEmail(10)); err != nil {
		t.Fatal(err)
	}

	form := fake.last("sendRichMessage")
	if form == nil {
		t.Fatalf("sendRichMessage not called, calls: %v", fake.methods())
	}
	if form["message_thread_id"] != "5" || form["chat_id"] != "-100" {
		t.Errorf("wrong target: %v", form)
	}
	var rich struct {
		HTML string `json:"html"`
	}
	json.Unmarshal([]byte(form["rich_message"]), &rich)
	if !strings.Contains(rich.HTML, "<code>482913</code>") {
		t.Errorf("rich html: %s", rich.HTML)
	}
	if !strings.Contains(form["reply_markup"], `"copy_text":{"text":"482913"}`) ||
		!strings.Contains(form["reply_markup"], `"style":"success"`) {
		t.Errorf("reply_markup: %s", form["reply_markup"])
	}

	acc, _ := db.GetAccountByID(context.Background(), account.ID)
	if acc.LastUID != 10 {
		t.Errorf("last uid = %d, want 10", acc.LastUID)
	}

	// The same UID again is a duplicate and is not sent twice
	before := len(fake.methods())
	if err := b.onNewEmail(account.ID, testEmail(10)); err != nil {
		t.Fatal(err)
	}
	if len(fake.methods()) != before {
		t.Error("duplicate email was sent again")
	}
}

func TestDeliverFallbacks(t *testing.T) {
	fake := &fakeTelegram{reply: func(method string, form map[string]string) (int, string) {
		if method == "sendRichMessage" {
			return 400, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse rich message"}`
		}
		return 0, ""
	}}
	b, _, account := newTestBot(t, fake)

	if err := b.onNewEmail(account.ID, testEmail(11)); err != nil {
		t.Fatal(err)
	}
	if form := fake.last("sendMessage"); form == nil || form["parse_mode"] != "HTML" {
		t.Fatalf("no HTML fallback, calls: %v", fake.methods())
	}
	if !b.richEnabled() {
		t.Error("a content error must not disable rich messages for good")
	}

	// A Bot API server without rich messages: fall back and stop trying
	fake.mu.Lock()
	fake.reply = func(method string, form map[string]string) (int, string) {
		if method == "sendRichMessage" {
			return 404, `{"ok":false,"error_code":404,"description":"Not Found: method not found"}`
		}
		return 0, ""
	}
	fake.mu.Unlock()
	if err := b.onNewEmail(account.ID, testEmail(12)); err != nil {
		t.Fatal(err)
	}
	if b.richEnabled() {
		t.Error("rich messages still enabled after 404")
	}
}

func TestDeliverTopicDeleted(t *testing.T) {
	fake := &fakeTelegram{reply: func(method string, form map[string]string) (int, string) {
		if form["message_thread_id"] == "5" {
			return 400, `{"ok":false,"error_code":400,"description":"Bad Request: message thread not found"}`
		}
		return 0, ""
	}}
	b, _, account := newTestBot(t, fake)

	if err := b.onNewEmail(account.ID, testEmail(13)); err != nil {
		t.Fatal(err)
	}
	form := fake.last("sendRichMessage")
	if form["message_thread_id"] != "" || !strings.Contains(form["rich_message"], "удалён") {
		t.Errorf("not redirected to the general topic with a notice: %v", form)
	}
}

func TestDeliverErrorsRetryOrSkip(t *testing.T) {
	status := 502
	fake := &fakeTelegram{reply: func(method string, form map[string]string) (int, string) {
		if strings.HasPrefix(method, "send") {
			return status, fmt.Sprintf(`{"ok":false,"error_code":%d,"description":"error"}`, status)
		}
		return 0, ""
	}}
	b, db, account := newTestBot(t, fake)

	// Telegram is down: the email must be retried later
	if err := b.onNewEmail(account.ID, testEmail(20)); err == nil {
		t.Fatal("expected an error to retry delivery")
	}
	if acc, _ := db.GetAccountByID(context.Background(), account.ID); acc.LastUID == 20 {
		t.Error("last uid advanced for an undelivered email")
	}

	// Bot removed from the group: retrying cannot help, skip the email
	status = 403
	if err := b.onNewEmail(account.ID, testEmail(20)); err != nil {
		t.Fatalf("permanent error must not be retried: %v", err)
	}
	if acc, _ := db.GetAccountByID(context.Background(), account.ID); acc.LastUID != 20 {
		t.Errorf("last uid = %d, want 20", acc.LastUID)
	}
}
