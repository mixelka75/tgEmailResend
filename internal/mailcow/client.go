package mailcow

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"
)

// Client is a Mailcow API client
type Client struct {
	baseURL    string
	apiKey     string
	domain     string
	httpClient *http.Client
}

// Config for Mailcow client
type Config struct {
	BaseURL string // e.g., https://mail.example.com
	APIKey  string
	Domain  string // default domain for new mailboxes
}

// Mailbox represents a mailbox in Mailcow
type Mailbox struct {
	LocalPart string `json:"local_part"`
	Domain    string `json:"domain"`
	Name      string `json:"name"`
	Password  string `json:"password,omitempty"`
	Quota     int    `json:"quota"` // MB
	Active    bool   `json:"active"`
}

// CreateMailboxRequest request for creating a mailbox
type CreateMailboxRequest struct {
	LocalPart       string `json:"local_part"`
	Domain          string `json:"domain"`
	Name            string `json:"name"`
	Password        string `json:"password"`
	Password2       string `json:"password2"`
	Quota           int    `json:"quota"`
	Active          int    `json:"active"`
	ForcePWUpdate   int    `json:"force_pw_update"`
	TLSEnforceIn    int    `json:"tls_enforce_in"`
	TLSEnforceOut   int    `json:"tls_enforce_out"`
	SOGoAccess      int    `json:"sogo_access"`
	IMAPAccess      int    `json:"imap_access"`
	POPAccess       int    `json:"pop3_access"`
	SMTPAccess      int    `json:"smtp_access"`
}

// APIResponse generic API response
type APIResponse struct {
	Type string        `json:"type"`
	Log  []interface{} `json:"log"`
	Msg  []string      `json:"msg"`
}

// NewClient creates a new Mailcow API client
func NewClient(cfg Config) *Client {
	return &Client{
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
		domain:  cfg.Domain,
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

// CreateMailbox creates a new mailbox
func (c *Client) CreateMailbox(ctx context.Context, localPart, name, password string, quotaMB int) (*Mailbox, error) {
	if !c.IsConfigured() {
		return nil, fmt.Errorf("mailcow not configured")
	}

	// Generate password if not provided
	if password == "" {
		var err error
		password, err = GenerateSecurePassword(16)
		if err != nil {
			return nil, fmt.Errorf("failed to generate password: %w", err)
		}
	}

	// Default quota 1GB
	if quotaMB <= 0 {
		quotaMB = 1024
	}

	req := CreateMailboxRequest{
		LocalPart:       localPart,
		Domain:          c.domain,
		Name:            name,
		Password:        password,
		Password2:       password,
		Quota:           quotaMB,
		Active:          1,
		ForcePWUpdate:   0,
		TLSEnforceIn:    1,
		TLSEnforceOut:   1,
		SOGoAccess:      1,
		IMAPAccess:      1,
		POPAccess:       1,
		SMTPAccess:      1,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/v1/add/mailbox", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error: %s (status %d)", string(respBody), resp.StatusCode)
	}

	// Mailcow API returns an array of responses
	var apiResp []APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w (body: %s)", err, string(respBody))
	}

	if len(apiResp) == 0 {
		return nil, fmt.Errorf("empty response from API")
	}

	if apiResp[0].Type != "success" {
		errMsg := "unknown error"
		if len(apiResp[0].Msg) > 0 {
			errMsg = apiResp[0].Msg[0]
		}
		return nil, fmt.Errorf("API error: %s", errMsg)
	}

	return &Mailbox{
		LocalPart: localPart,
		Domain:    c.domain,
		Name:      name,
		Password:  password,
		Quota:     quotaMB,
		Active:    true,
	}, nil
}

// DeleteMailbox deletes a mailbox
func (c *Client) DeleteMailbox(ctx context.Context, email string) error {
	if !c.IsConfigured() {
		return fmt.Errorf("mailcow not configured")
	}

	body, err := json.Marshal([]string{email})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/v1/delete/mailbox", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API error: %s (status %d)", string(respBody), resp.StatusCode)
	}

	// Mailcow API returns an array of responses
	var apiResp []APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if len(apiResp) == 0 {
		return fmt.Errorf("empty response from API")
	}

	if apiResp[0].Type != "success" {
		errMsg := "unknown error"
		if len(apiResp[0].Msg) > 0 {
			errMsg = apiResp[0].Msg[0]
		}
		return fmt.Errorf("API error: %s", errMsg)
	}

	return nil
}

// GenerateSecurePassword generates a cryptographically secure password
func GenerateSecurePassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"

	password := make([]byte, length)
	for i := range password {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		password[i] = charset[num.Int64()]
	}

	return string(password), nil
}

// GetIMAPServer returns the IMAP server for the Mailcow domain
func (c *Client) GetIMAPServer() string {
	// Extract host from baseURL
	// e.g., https://mail.example.com -> mail.example.com:993
	host := c.baseURL
	if len(host) > 8 && host[:8] == "https://" {
		host = host[8:]
	} else if len(host) > 7 && host[:7] == "http://" {
		host = host[7:]
	}

	// Remove trailing slash
	if host[len(host)-1] == '/' {
		host = host[:len(host)-1]
	}

	return host + ":993"
}
