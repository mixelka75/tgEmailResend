package formatter

import (
	"fmt"
	"html"
	"strings"

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

// FormatEmail formats an email message for Telegram
func (f *TelegramFormatter) FormatEmail(msg *models.EmailMessage, codes []models.DetectedCode) string {
	var sb strings.Builder

	// Header
	from := msg.FromAddr
	if msg.FromName != "" {
		from = fmt.Sprintf("%s &lt;%s&gt;", f.escapeHTML(msg.FromName), f.escapeHTML(msg.FromAddr))
	} else {
		from = f.escapeHTML(from)
	}

	sb.WriteString(fmt.Sprintf("<b>От:</b> %s\n", from))
	subject, _ := f.truncate(msg.Subject, 300)
	sb.WriteString(fmt.Sprintf("<b>Тема:</b> %s\n", f.escapeHTML(subject)))
	sb.WriteString(fmt.Sprintf("<b>Дата:</b> %s\n", msg.ReceivedAt.Format("02.01.2006 15:04")))
	sb.WriteString("\n")

	// Detected codes section
	if len(codes) > 0 {
		sb.WriteString("<b>Коды:</b>\n")
		for _, code := range codes {
			sb.WriteString(fmt.Sprintf("<code>%s</code> ", f.escapeHTML(code.Value)))
		}
		sb.WriteString("\n\n")
	}

	// Body. Telegram counts the limit in characters of the text after
	// entities are parsed, so count runes of the unescaped text.
	header := sb.String()
	sb.WriteString("<b>Сообщение:</b>\n")
	headerRunes := len([]rune(stripTags(header))) + len("Сообщение:\n")
	body, truncated := f.truncate(strings.TrimSpace(msg.BodyText), f.maxLength-headerRunes-50)
	if body == "" {
		body = "(пустое письмо)"
	}
	sb.WriteString(f.escapeHTML(body))
	if truncated {
		sb.WriteString("\n\n<i>... (сообщение обрезано)</i>")
	}

	return sb.String()
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
