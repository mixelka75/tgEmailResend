package formatter

import (
	"fmt"
	"html"
	"strings"
	"unicode/utf8"

	"github.com/mixelka/emailresend/pkg/models"
)

// TelegramFormatter formats emails for Telegram
type TelegramFormatter struct {
	maxLength int
}

// NewTelegramFormatter creates a new Telegram formatter
func NewTelegramFormatter() *TelegramFormatter {
	return &TelegramFormatter{
		maxLength: 4000, // Leave room for markup
	}
}

// FormatEmail formats an email for sendMessage with HTML parse mode.
// It is the fallback when a rich message cannot be sent.
func (f *TelegramFormatter) FormatEmail(msg *models.EmailMessage, codes []models.DetectedCode) string {
	var sb strings.Builder

	subject := strings.TrimSpace(msg.Subject)
	if subject == "" {
		subject = "(без темы)"
	}
	subject, _ = f.truncate(subject, 300)

	sb.WriteString(fmt.Sprintf("<b>%s</b>\n", f.escapeHTML(subject)))
	sb.WriteString(fmt.Sprintf("<b>От:</b> %s\n", f.formatFrom(msg)))
	sb.WriteString(fmt.Sprintf("<b>Дата:</b> %s\n", formatTime(msg)))

	if len(codes) > 0 {
		sb.WriteString("<b>Код:</b>")
		for _, code := range codes {
			sb.WriteString(" <code>" + f.escapeHTML(code.Value) + "</code>")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// Telegram counts the limit in characters of the text after entities
	// are parsed, so count runes of the unescaped text
	headerRunes := len([]rune(stripTags(sb.String())))
	body, truncated := f.truncate(strings.TrimSpace(msg.BodyText), f.maxLength-headerRunes-60)
	if body == "" {
		sb.WriteString("<i>(пустое письмо)</i>")
		return sb.String()
	}

	// Long bodies are collapsed so the chat stays readable
	if utf8.RuneCountInString(body) > 800 {
		sb.WriteString("<blockquote expandable>" + f.escapeHTML(body) + "</blockquote>")
	} else {
		sb.WriteString(f.escapeHTML(body))
	}
	if truncated {
		sb.WriteString("\n<i>… сообщение обрезано</i>")
	}

	return sb.String()
}

// PlainEmail formats an email without any markup: the last resort when
// Telegram rejects formatted text
func (f *TelegramFormatter) PlainEmail(msg *models.EmailMessage) string {
	text := fmt.Sprintf("%s\nОт: %s <%s>\nДата: %s\n\n%s",
		msg.Subject, msg.FromName, msg.FromAddr, msg.ReceivedAt.Format("02.01.2006 15:04"), msg.BodyText)
	text, _ = f.truncate(text, f.maxLength)
	return text
}

// escapeHTML escapes HTML special characters for Telegram
func (f *TelegramFormatter) escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// truncate truncates text to maxLen characters
func (f *TelegramFormatter) truncate(s string, maxLen int) (string, bool) {
	if maxLen <= 0 {
		maxLen = 100
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s, false
	}
	return string(runes[:maxLen]), true
}

// stripTags approximates the visible text of an HTML-formatted message
func stripTags(s string) string {
	var sb strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			sb.WriteRune(r)
		}
	}
	return html.UnescapeString(sb.String())
}
