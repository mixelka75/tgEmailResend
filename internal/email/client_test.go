package email

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/emersion/go-imap"
)

func TestParseMessageNonUTF8(t *testing.T) {
	raw, err := os.ReadFile("testdata/cp1251.eml")
	if err != nil {
		t.Fatal(err)
	}

	section := &imap.BodySectionName{Peek: true}
	msg := &imap.Message{
		Uid:  7,
		Body: map[*imap.BodySectionName]imap.Literal{{}: bytes.NewReader(raw)},
	}

	c := NewClient(ClientConfig{Email: "test"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	email := c.parseMessage(msg, section)

	if email.BodyText != "Ваш код: 482913" {
		t.Errorf("BodyText = %q", email.BodyText)
	}
	if email.BodyHTML != "<p>Привет</p>" {
		t.Errorf("BodyHTML = %q", email.BodyHTML)
	}
}
