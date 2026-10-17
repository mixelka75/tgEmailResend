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
