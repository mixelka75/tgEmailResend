package email

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// Common IMAP servers for popular email providers
var knownIMAPServers = map[string]string{
	"gmail.com":       "imap.gmail.com:993",
	"googlemail.com":  "imap.gmail.com:993",
	"outlook.com":     "outlook.office365.com:993",
	"hotmail.com":     "outlook.office365.com:993",
	"live.com":        "outlook.office365.com:993",
	"msn.com":         "outlook.office365.com:993",
	"yahoo.com":       "imap.mail.yahoo.com:993",
	"yahoo.co.uk":     "imap.mail.yahoo.com:993",
	"yandex.ru":       "imap.yandex.ru:993",
	"yandex.com":      "imap.yandex.com:993",
	"mail.ru":         "imap.mail.ru:993",
	"bk.ru":           "imap.mail.ru:993",
	"list.ru":         "imap.mail.ru:993",
	"inbox.ru":        "imap.mail.ru:993",
	"icloud.com":      "imap.mail.me.com:993",
	"me.com":          "imap.mail.me.com:993",
	"mac.com":         "imap.mail.me.com:993",
	"aol.com":         "imap.aol.com:993",
	"zoho.com":        "imap.zoho.com:993",
	"protonmail.com":  "127.0.0.1:1143", // ProtonMail Bridge
	"proton.me":       "127.0.0.1:1143",
	"fastmail.com":    "imap.fastmail.com:993",
	"gmx.com":         "imap.gmx.com:993",
	"gmx.de":          "imap.gmx.net:993",
	"web.de":          "imap.web.de:993",
	"t-online.de":     "secureimap.t-online.de:993",
	"rambler.ru":      "imap.rambler.ru:993",
}

// ResolveIMAPServer determines the IMAP server for an email address
func ResolveIMAPServer(email string) (string, error) {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid email format")
	}

	domain := strings.ToLower(parts[1])

	// Check known providers first
	if server, ok := knownIMAPServers[domain]; ok {
		return server, nil
	}

	// Try common IMAP server patterns
	patterns := []string{
		"imap." + domain + ":993",
		"mail." + domain + ":993",
		domain + ":993",
	}

	for _, pattern := range patterns {
		host := strings.TrimSuffix(pattern, ":993")
		if checkIMAPServer(host, 993) {
			return pattern, nil
		}
	}

	// Try to resolve via MX records
	mxServer, err := resolveViaMX(domain)
	if err == nil && mxServer != "" {
		return mxServer, nil
	}

	// Default fallback - try imap.domain:993
	return "imap." + domain + ":993", nil
}

// checkIMAPServer checks if an IMAP server is reachable
func checkIMAPServer(host string, port int) bool {
	address := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// resolveViaMX tries to determine IMAP server from MX records
func resolveViaMX(domain string) (string, error) {
	mxRecords, err := net.LookupMX(domain)
	if err != nil || len(mxRecords) == 0 {
		return "", fmt.Errorf("no MX records found")
	}

	// Get the primary MX record
	mxHost := strings.TrimSuffix(mxRecords[0].Host, ".")

	// Try to derive IMAP server from MX host
	// e.g., mx.example.com -> imap.example.com
	parts := strings.SplitN(mxHost, ".", 2)
	if len(parts) == 2 {
		baseDomain := parts[1]
		imapHost := "imap." + baseDomain
		if checkIMAPServer(imapHost, 993) {
			return imapHost + ":993", nil
		}

		// Try mail.domain
		mailHost := "mail." + baseDomain
		if checkIMAPServer(mailHost, 993) {
			return mailHost + ":993", nil
		}
	}

	return "", fmt.Errorf("could not determine IMAP server")
}

// GetDomainFromEmail extracts domain from email address
func GetDomainFromEmail(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return ""
	}
	return strings.ToLower(parts[1])
}
