package reminder

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ntfyAttempts is how many times Ntfy tries a send that fails for a reason
// that may pass (the server unreachable, a 5xx, or 429).
const ntfyAttempts = 3

// Ntfy delivers notifications by publishing to a topic on an ntfy server
// with coreban's access token.
type Ntfy struct {
	// URL is the server's base URL; the topic is appended as a path.
	URL   string
	Token string
	// Backoff is the wait before the first retry, doubling after; zero
	// means one second.
	Backoff time.Duration
	// Client defaults to one with a ten-second timeout.
	Client *http.Client
}

var defaultNtfyClient = &http.Client{Timeout: 10 * time.Second}

// Notify publishes n to its topic: the message as the body, titled
// "Reminder", or "Late reminder" with its moment appended. A failure that
// outlasts every try but may still pass wraps ErrRetryLater.
func (s *Ntfy) Notify(ctx context.Context, n Notification) error {
	endpoint, err := url.JoinPath(s.URL, n.Topic)
	if err != nil {
		return fmt.Errorf("ntfy URL: %w", err)
	}
	title, tags, body := "Reminder", "alarm_clock", n.Message
	if n.Late {
		title, tags = "Late reminder", "hourglass"
		body += "\n\nIt was set for " + n.At.Format("Mon 2 Jan 15:04 MST") + "."
	}
	client := s.Client
	if client == nil {
		client = defaultNtfyClient
	}
	wait := s.Backoff
	if wait == 0 {
		wait = time.Second
	}

	for attempt := 1; ; attempt++ {
		retry, err := s.publish(ctx, client, endpoint, title, tags, body)
		if err == nil || !retry {
			return err
		}
		if attempt == ntfyAttempts {
			return fmt.Errorf("%w: %w", ErrRetryLater, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
	}
}

// publish makes one attempt and reports whether a failure may pass.
func (s *Ntfy) publish(ctx context.Context, client *http.Client, endpoint, title, tags, body string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", title)
	req.Header.Set("Tags", tags)
	resp, err := client.Do(req)
	if err != nil {
		return ctx.Err() == nil, fmt.Errorf("ntfy unreachable: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 == 2 {
		return false, nil
	}
	retry := resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests
	return retry, fmt.Errorf("ntfy answered %s", resp.Status)
}
