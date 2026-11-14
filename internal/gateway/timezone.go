// Package gateway asks coregateway what coreban's background work needs to
// know about a user when there is no internal token to read it from.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	// The runtime image ships without zoneinfo; embed it so every zone the
	// gateway answers resolves the same everywhere.
	_ "time/tzdata"

	"github.com/google/uuid"
)

// requestTimeout bounds one lookup, so a hung gateway delays a dispatch
// pass by at most this much per owner.
const requestTimeout = 10 * time.Second

// Client calls the gateway with an API key minted for coreban's background
// work: owned by an admin and holding coreban:admin (the gateway's
// docs/auth-flow.md, "A user's timezone").
type Client struct {
	// URL is the gateway's base URL, such as http://coregateway-api:8080.
	URL    string
	APIKey string
	// HTTP defaults to a client with requestTimeout.
	HTTP *http.Client
}

// Timezone looks up user's timezone (the gateway's ADR-0005). A user who
// never set one is UTC. Every other outcome, including a zone this binary
// can't load, is an error: callers fail closed rather than guess a zone.
func (c *Client) Timezone(ctx context.Context, user uuid.UUID) (*time.Location, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(c.URL, "/")+"/api/users/"+user.String()+"/timezone", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: requestTimeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gateway timezone of %s: %w", user, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return nil, fmt.Errorf("gateway timezone of %s: read: %w", user, err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return nil, fmt.Errorf("gateway timezone of %s: status %d: %s", user, resp.StatusCode, e.Error)
	}
	var v struct {
		Timezone string `json:"timezone"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("gateway timezone of %s: decode: %w", user, err)
	}
	if v.Timezone == "" || v.Timezone == "Local" {
		return nil, fmt.Errorf("gateway timezone of %s: no usable timezone in %q", user, body)
	}
	loc, err := time.LoadLocation(v.Timezone)
	if err != nil {
		return nil, fmt.Errorf("gateway timezone of %s: %w", user, err)
	}
	return loc, nil
}
