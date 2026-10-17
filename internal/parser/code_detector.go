package parser

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/mixelka/emailresend/pkg/models"
)

// maxCodes limits how many codes are shown for one email
const maxCodes = 4

// keywords that usually precede a verification code
const codeKeywords = `code|код|otp|pin|пин|пароль|password|passcode|verification|верификац|подтвержд|confirm|активац|security|безопасност|2fa|одноразов`

// CodeDetector detects verification codes in text
type CodeDetector struct {
	patterns []*codePattern
}

type codePattern struct {
	Type  string
	Regex *regexp.Regexp
	// Valid filters out false positives; nil accepts everything
	Valid func(code string) bool
}

// NewCodeDetector creates a new code detector
func NewCodeDetector() *CodeDetector {
	return &CodeDetector{
		patterns: []*codePattern{
			// Numeric code after a keyword on the same line:
			// "Код подтверждения: 123456", "Your verification code is 1234"
			{
				Type:  "otp",
				Regex: regexp.MustCompile(`(?i)(?:` + codeKeywords + `)[^\n]{0,40}?(?:^|[^\d])(\d{4,8})\b`),
			},
			// Split numeric code: "123 456", "123-456"
			{
				Type:  "otp",
				Regex: regexp.MustCompile(`(?i)(?:` + codeKeywords + `)[^\n]{0,40}?(?:^|[^\d])(\d{3}[ \-]\d{3})\b`),
			},
			// Standalone numeric code on its own line
			{
				Type:  "code",
				Regex: regexp.MustCompile(`(?m)^[^\S\n]*(\d{4,8})[^\S\n]*$`),
				Valid: notYear,
			},
			// Alphanumeric code: "Code: AB12CD"
			{
				Type:  "code",
				Regex: regexp.MustCompile(`(?i:code|код)[\s:\-]+([A-Z0-9]{4,12})\b`),
				Valid: hasDigitAndLetter,
			},
			// Token/key: "Token: abc123def456"
			{
				Type:  "token",
				Regex: regexp.MustCompile(`(?i:token|токен|key|ключ)[\s:\-]+([A-Za-z0-9\-_]{8,32})\b`),
				Valid: hasDigitAndLetter,
			},
		},
	}
}

// DetectCodes finds all verification codes in text
func (d *CodeDetector) DetectCodes(text string) []models.DetectedCode {
	var codes []models.DetectedCode
	seen := make(map[string]bool)

	for _, pattern := range d.patterns {
		matches := pattern.Regex.FindAllStringSubmatch(text, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			code := normalizeCode(match[1])
			if len(code) < 4 || seen[code] {
				continue
			}
			if pattern.Valid != nil && !pattern.Valid(code) {
				continue
			}
			seen[code] = true
			codes = append(codes, models.DetectedCode{
				Type:  pattern.Type,
				Value: code,
			})
			if len(codes) >= maxCodes {
				return codes
			}
		}
	}

	return codes
}

// normalizeCode removes separators from split numeric codes ("123 456" -> "123456")
func normalizeCode(code string) string {
	code = strings.TrimSpace(code)
	stripped := strings.NewReplacer(" ", "", "-", "").Replace(code)
	if _, err := strconv.Atoi(stripped); err == nil {
		return stripped
	}
	return code
}

// notYear rejects 4-digit numbers that look like years (footers, dates)
func notYear(code string) bool {
	if len(code) != 4 {
		return true
	}
	n, _ := strconv.Atoi(code)
	return n < 1900 || n > 2099
}

func hasDigitAndLetter(code string) bool {
	return strings.IndexFunc(code, unicode.IsDigit) >= 0 && strings.IndexFunc(code, unicode.IsLetter) >= 0
}
