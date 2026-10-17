package mailcow

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Client is a Mailcow API client.
// API reference: https://mailcow.docs.apiary.io and data/web/api/openapi.yaml
// in mailcow-dockerized.
type Client struct {
	baseURL    string
	apiKey     string
	domain     string
	imapServer string
	quotaMB    int
	httpClient *http.Client
}

// Config for Mailcow client
type Config struct {
	BaseURL    string // e.g., https://mail.example.com
	APIKey     string // read-write API key
	Domain     string // domain for new mailboxes
	IMAPServer string // host:port of the IMAP server (default: BaseURL host, port 993)
	QuotaMB    int    // quota of new mailboxes, 0 = the domain's default
}

// Mailbox represents a created mailbox
type Mailbox struct {
	LocalPart string
	Domain    string
	Name      string
	Password  string
}

// Email returns the full address of the mailbox
func (m *Mailbox) Email() string {
	return m.LocalPart + "@" + m.Domain
}

// apiResult is one entry of a Mailcow API response. "msg" is either a
// string or an array: a message key followed by its arguments.
type apiResult struct {
	Type string          `json:"type"`
	Msg  json.RawMessage `json:"msg"`
}

// APIError is an error reported by the Mailcow API
type APIError struct {
	Key  string   // message key, e.g. "object_exists"
	Args []string // message arguments
}

func (e *APIError) Error() string {
	if ip, ok := strings.CutPrefix(e.Key, "api access denied for ip "); ok {
		return "IP бота (" + ip + ") не добавлен в список разрешённых для API в настройках Mailcow"
	}
	if text, ok := errorTexts[e.Key]; ok {
		return fmt.Sprintf(text, toAny(e.Args)...)
	}
	if len(e.Args) > 0 {
		return e.Key + ": " + strings.Join(e.Args, ", ")
	}
	return e.Key
}

func toAny(args []string) []any {
	out := make([]any, 4)
	for i := range out {
		out[i] = ""
		if i < len(args) {
			out[i] = args[i]
		}
	}
	return out
}

// Russian texts for common Mailcow message keys (data/web/lang/lang.*.json)
var errorTexts = map[string]string{
	"object_exists":                "ящик %s уже существует",
	"is_alias":                     "%s уже используется как алиас",
	"is_spam_alias":                "%s уже используется как спам-алиас",
	"domain_not_found":             "домен %s не найден в Mailcow",
	"domain_invalid":               "некорректное имя домена",
	"mailbox_invalid":              "некорректное имя ящика",
	"access_denied":                "у API-ключа нет доступа к этому домену",
	"max_mailbox_exceeded":         "превышен лимит ящиков домена (%s из %s)",
	"mailbox_quota_exceeded":       "квота превышает максимум для ящика домена (%s МБ)",
	"mailbox_quota_left_exceeded":  "на домене осталось только %s МБ квоты",
	"unlimited_quota_acl":          "безлимитная квота запрещена: задайте MAILCOW_MAILBOX_QUOTA_MB",
	"password_complexity":          "пароль не соответствует политике сложности Mailcow",
	"password_mismatch":            "пароли не совпадают",
	"authentication failed":        "неверный или отключённый MAILCOW_API_KEY",
	"API read/write access denied": "у API-ключа только права на чтение, нужен ключ read-write",
	"route not found":              "API не найден, проверьте MAILCOW_URL",
}

// NewClient creates a new Mailcow API client
func NewClient(cfg Config) *Client {
	return &Client{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:     cfg.APIKey,
		domain:     strings.ToLower(cfg.Domain),
		imapServer: cfg.IMAPServer,
		quotaMB:    cfg.QuotaMB,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// IsConfigured returns true if Mailcow integration is configured
func (c *Client) IsConfigured() bool {
	return c.baseURL != "" && c.apiKey != "" && c.domain != ""
}

// GetDomain returns the configured domain
func (c *Client) GetDomain() string {
	return c.domain
}

var localPartRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9._+-]{0,62}[a-z0-9])?$`)

// ValidateLocalPart checks the part of an address before "@"
func ValidateLocalPart(localPart string) error {
	if !localPartRegex.MatchString(localPart) || strings.Contains(localPart, "..") {
		return errors.New("имя ящика может содержать только латинские буквы, цифры, точку, дефис, подчёркивание и плюс")
	}
	return nil
}

// Check verifies the API key and that the domain exists
func (c *Client) Check(ctx context.Context) error {
	var domain map[string]any
	body, err := c.request(ctx, http.MethodGet, "/api/v1/get/domain/"+url.PathEscape(c.domain), nil)
	if err != nil {
		return err
	}
	// A missing domain is returned as {} or []
	if json.Unmarshal(body, &domain) != nil || domain["domain_name"] == nil {
		return &APIError{Key: "domain_not_found", Args: []string{c.domain}}
	}
	if active := fmt.Sprint(domain["active"]); active == "0" || active == "false" {
		return fmt.Errorf("домен %s отключён в Mailcow", c.domain)
	}
	return nil
}

// CreateMailbox creates a new mailbox. An empty password is generated.
func (c *Client) CreateMailbox(ctx context.Context, localPart, name, password string) (*Mailbox, error) {
	if !c.IsConfigured() {
		return nil, errors.New("mailcow not configured")
	}
	if err := ValidateLocalPart(localPart); err != nil {
		return nil, err
	}

	if password == "" {
		var err error
		password, err = GenerateSecurePassword(20)
		if err != nil {
			return nil, fmt.Errorf("failed to generate password: %w", err)
		}
	}

	// Fields not set here take the domain's / server's defaults
	// (quota, TLS enforcement, other protocols)
	req := map[string]any{
		"local_part":      localPart,
		"domain":          c.domain,
		"name":            name,
		"password":        password,
		"password2":       password,
		"active":          "1",
		"force_pw_update": "0",
		"imap_access":     "1", // the bot reads mail over IMAP
	}
	if c.quotaMB > 0 {
		req["quota"] = fmt.Sprint(c.quotaMB)
	}

	body, err := c.request(ctx, http.MethodPost, "/api/v1/add/mailbox", req)
	if err != nil {
		return nil, err
	}
	if err := checkResults(body); err != nil {
		return nil, err
	}

	return &Mailbox{
		LocalPart: localPart,
		Domain:    c.domain,
		Name:      name,
		Password:  password,
	}, nil
}

// DeleteMailbox deletes a mailbox
func (c *Client) DeleteMailbox(ctx context.Context, email string) error {
	if !c.IsConfigured() {
		return errors.New("mailcow not configured")
	}

	body, err := c.request(ctx, http.MethodPost, "/api/v1/delete/mailbox", []string{email})
	if err != nil {
		return err
	}
	return checkResults(body)
}

// request performs an API call and returns the response body
func (c *Client) request(ctx context.Context, method, path string, payload any) ([]byte, error) {
	var reqBody io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if payload != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Mailcow недоступен: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Errors come as {"type":"error","msg":"authentication failed"}
		var single apiResult
		if json.Unmarshal(body, &single) == nil && single.Type != "" {
			return nil, resultError(single)
		}
		return nil, fmt.Errorf("Mailcow API ответил %d: %s", resp.StatusCode, snippet(body))
	}

	if !json.Valid(body) {
		// Usually a wrong MAILCOW_URL: an HTML page instead of the API
		return nil, fmt.Errorf("Mailcow API вернул не JSON (проверьте MAILCOW_URL): %s", snippet(body))
	}
	return body, nil
}

// checkResults returns the first error in a Mailcow API response. Responses
// are arrays of results; a single object is accepted too.
func checkResults(body []byte) error {
	var results []apiResult
	if err := json.Unmarshal(body, &results); err != nil {
		var single apiResult
		if errSingle := json.Unmarshal(body, &single); errSingle != nil {
			return fmt.Errorf("failed to parse response: %w (body: %s)", err, snippet(body))
		}
		results = []apiResult{single}
	}
	if len(results) == 0 {
		return errors.New("empty response from Mailcow API")
	}
	for _, r := range results {
		if r.Type != "success" {
			return resultError(r)
		}
	}
	return nil
}

func resultError(r apiResult) error {
	var text string
	if json.Unmarshal(r.Msg, &text) == nil {
		return &APIError{Key: text}
	}
	var parts []any
	if json.Unmarshal(r.Msg, &parts) == nil && len(parts) > 0 {
		e := &APIError{Key: fmt.Sprint(parts[0])}
		for _, p := range parts[1:] {
			e.Args = append(e.Args, fmt.Sprint(p))
		}
		return e
	}
	return &APIError{Key: "unknown error: " + snippet(r.Msg)}
}

// snippet shortens a response body for error messages
func snippet(body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// GenerateSecurePassword generates a password that satisfies any Mailcow
// password policy: lower and upper case letters, digits and special characters
func GenerateSecurePassword(length int) (string, error) {
	const (
		lower   = "abcdefghijkmnopqrstuvwxyz"
		upper   = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		digits  = "23456789"
		special = "!@#%^*-_=+"
	)
	if length < 8 {
		length = 8
	}

	pick := func(charset string) (byte, error) {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return 0, err
		}
		return charset[n.Int64()], nil
	}

	password := make([]byte, 0, length)
	for _, set := range []string{lower, upper, digits, special} {
		ch, err := pick(set)
		if err != nil {
			return "", err
		}
		password = append(password, ch)
	}
	all := lower + upper + digits + special
	for len(password) < length {
		ch, err := pick(all)
		if err != nil {
			return "", err
		}
		password = append(password, ch)
	}

	// Shuffle so the required characters are not always first
	for i := len(password) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", err
		}
		password[i], password[j.Int64()] = password[j.Int64()], password[i]
	}

	return string(password), nil
}

// GetIMAPServer returns the IMAP server of created mailboxes
func (c *Client) GetIMAPServer() string {
	if c.imapServer != "" {
		if _, _, err := net.SplitHostPort(c.imapServer); err == nil {
			return c.imapServer
		}
		return net.JoinHostPort(c.imapServer, "993")
	}
	// e.g., https://mail.example.com/ -> mail.example.com:993
	host := c.baseURL
	if u, err := url.Parse(c.baseURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return net.JoinHostPort(strings.Trim(host, "/"), "993")
}
