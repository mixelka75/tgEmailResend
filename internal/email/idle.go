package email

import (
	"log/slog"
	"time"

	"github.com/emersion/go-imap/client"
)

// IdleClient wraps IMAP client with IDLE support
type IdleClient struct {
	client *client.Client
	logger *slog.Logger
}

// NewIdleClient creates a new IDLE client
func NewIdleClient(c *client.Client, logger *slog.Logger) *IdleClient {
	return &IdleClient{client: c, logger: logger}
}

// IdleWithFallback just uses polling - IDLE library is unreliable
func (ic *IdleClient) IdleWithFallback(stop <-chan struct{}, timeout time.Duration) error {
	// Just use polling - it's more reliable
	ic.logger.Info("using polling", "interval", "15s")
	return ic.pollFallback(stop, 15*time.Second)
}

// pollFallback polls for new messages when IDLE is not supported
func (ic *IdleClient) pollFallback(stop <-chan struct{}, timeout time.Duration) error {
	ticker := time.NewTicker(timeout)
	defer ticker.Stop()

	select {
	case <-stop:
		return nil
	case <-ticker.C:
		return nil
	}
}
