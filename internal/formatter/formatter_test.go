package formatter

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mixelka/emailresend/pkg/models"
)

func TestFormatEmailEscapesAndTruncates(t *testing.T) {
	f := NewTelegramFormatter()
	msg := &models.EmailMessage{
		FromAddr: "a@b.c",
		FromName: "Tom & <Jerry>",
		Subject:  "<script>",
		BodyText: strings.Repeat("я&", 5000),
	}

	text := f.FormatEmail(msg, nil)

	if strings.Contains(text, "<script>") || strings.Contains(text, "<Jerry>") {
		t.Errorf("user content is not escaped: %q", text[:200])
	}
	if !strings.HasSuffix(text, "<i>… сообщение обрезано</i>") {
		t.Errorf("truncation marker must stay real HTML, got tail %q", text[len(text)-60:])
	}
	if n := utf8.RuneCountInString(stripTags(text)); n > 4096 {
		t.Errorf("visible text is %d characters, Telegram limit is 4096", n)
	}
}

func TestKeyboardJSON(t *testing.T) {
	kb := BuildEmailKeyboard(42, []models.DetectedCode{{Type: "otp", Value: "123456"}}, false)
	raw, err := json.Marshal(kb)
	if err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		InlineKeyboard [][]map[string]any `json:"inline_keyboard"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}

	// Telegram requires exactly one action field per button
	for _, row := range parsed.InlineKeyboard {
		for _, btn := range row {
			actions := 0
			for _, k := range []string{"callback_data", "copy_text", "url"} {
				if _, ok := btn[k]; ok {
					actions++
				}
			}
			if actions != 1 {
				t.Errorf("button %v has %d action fields, want 1", btn, actions)
			}
			if data, ok := btn["callback_data"].(string); ok && len(data) > 64 {
				t.Errorf("callback_data %q exceeds 64 bytes", data)
			}
		}
	}

	codeBtn := parsed.InlineKeyboard[0][0]
	if codeBtn["copy_text"].(map[string]any)["text"] != "123456" {
		t.Errorf("code button must copy the code, got %v", codeBtn)
	}
}
