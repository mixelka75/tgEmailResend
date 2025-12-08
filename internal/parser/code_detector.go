package parser

import (
	"regexp"
	"strings"

	"github.com/mixelka/emailresend/pkg/models"
)

// CodeDetector detects verification codes in text
type CodeDetector struct {
	patterns []*codePattern
}

type codePattern struct {
	Type  string
	Regex *regexp.Regexp
}

// NewCodeDetector creates a new code detector
func NewCodeDetector() *CodeDetector {
	return &CodeDetector{
		patterns: []*codePattern{
			// OTP codes with keyword (4-8 digits)
			{
				Type:  "otp",
				Regex: regexp.MustCompile(`(?i)(?:code|код|otp|pin|пин|пароль|password)[\s:\-]*(\d{4,8})\b`),
			},
			// Verification codes
			{
				Type:  "verification",
				Regex: regexp.MustCompile(`(?i)(?:verification|верификац|подтвержд|confirm|активац)[\s\w]*[\s:\-]*(\d{4,8})\b`),
			},
			// Standalone numeric codes (4-8 digits on their own line)
			{
				Type:  "code",
				Regex: regexp.MustCompile(`(?m)^\s*(\d{4,8})\s*$`),
			},
			// Alphanumeric codes (like reset tokens)
			{
				Type:  "code",
				Regex: regexp.MustCompile(`(?i)(?:code|код)[\s:\-]*([A-Z0-9]{4,12})\b`),
			},
			// Security codes in specific format
			{
				Type:  "security",
				Regex: regexp.MustCompile(`(?i)(?:security|безопасност|2fa|two.factor)[\s\w]*[\s:\-]*(\d{4,8})\b`),
			},
			// Token/key patterns
			{
				Type:  "token",
				Regex: regexp.MustCompile(`(?i)(?:token|токен|key|ключ)[\s:\-]*([A-Za-z0-9\-_]{8,32})\b`),
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
			if len(match) > 1 {
				code := strings.TrimSpace(match[1])
				// Skip if already seen or too short
				if seen[code] || len(code) < 4 {
					continue
				}
				seen[code] = true
				codes = append(codes, models.DetectedCode{
					Type:  pattern.Type,
					Value: code,
				})
			}
		}
	}

	return codes
}
