package database

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/mixelka/emailresend/pkg/models"
)

func TestAccountsAndMessages(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := t.Context()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	acc := &models.EmailAccount{Email: "a@b.c", Password: "x", IMAPServer: "imap.b.c:993", ChatID: -100, TopicID: 5, IsActive: true, CreatedBy: 1}
	if err := db.CreateAccount(ctx, acc); err != nil {
		t.Fatal(err)
	}

	dup := *acc
	dup.TopicID = 6
	if err := db.CreateAccount(ctx, &dup); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate email in chat: err = %v, want ErrAlreadyExists", err)
	}

	if err := db.UpdateAccountLastUID(ctx, acc.ID, 42); err != nil {
		t.Fatal(err)
	}
	active, err := db.GetAllActiveAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].LastUID != 42 || active[0].TopicID != 5 {
		t.Fatalf("unexpected accounts: %+v", active)
	}

	msg := &models.EmailMessage{AccountID: acc.ID, UID: 43, FromAddr: "x@y.z", DetectedCodes: "[]"}
	if err := db.CreateMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateMessage(ctx, &models.EmailMessage{AccountID: acc.ID, UID: 43, FromAddr: "x@y.z"}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate message: err = %v", err)
	}
	if _, err := db.GetMessageByID(ctx, msg.ID); err != nil {
		t.Fatal(err)
	}

	// Disconnect removes the account's messages too
	if err := db.DeleteAccount(ctx, acc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetMessageByID(ctx, msg.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("message after account delete: err = %v, want ErrNotFound", err)
	}
}

func TestMigrateFromOldSchema(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := t.Context()

	// Schema of the first release, without uid_validity
	_, err = db.ExecContext(ctx, `CREATE TABLE email_accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL, password TEXT NOT NULL,
		imap_server TEXT NOT NULL, chat_id INTEGER NOT NULL, topic_id INTEGER NOT NULL,
		is_active BOOLEAN DEFAULT true, last_uid INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		created_by INTEGER NOT NULL, UNIQUE(chat_id, email), UNIQUE(chat_id, topic_id));
		INSERT INTO email_accounts (email, password, imap_server, chat_id, topic_id, last_uid, created_by)
		VALUES ('old@b.c', 'x', 'imap.b.c:993', -1, 2, 99, 1);`)
	if err != nil {
		t.Fatal(err)
	}

	// Twice: migrations must be idempotent
	for i := 0; i < 2; i++ {
		if err := db.Migrate(ctx); err != nil {
			t.Fatalf("migrate #%d: %v", i+1, err)
		}
	}

	accounts, err := db.GetAllActiveAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].LastUID != 99 || accounts[0].UIDValidity != 0 {
		t.Fatalf("unexpected accounts after migration: %+v", accounts)
	}
	if err := db.UpdateAccountSync(ctx, accounts[0].ID, 100, 555); err != nil {
		t.Fatal(err)
	}
	acc, _ := db.GetAccountByID(ctx, accounts[0].ID)
	if acc.LastUID != 100 || acc.UIDValidity != 555 {
		t.Fatalf("sync state not saved: %+v", acc)
	}
}
