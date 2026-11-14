package testharness

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/gateway"
)

// gatewayKey is the API key the fake gateway accepts, standing in for the
// admin-owned coreban:admin key the dispatcher runs with.
const gatewayKey = "cgw_live_testharness"

// Gateway is a stand-in for coregateway's user timezone lookup
// (GET /api/users/{id}/timezone), shared by every test in the binary like
// the database. Each test sets only its own owners' zones.
type Gateway struct {
	mu    sync.Mutex
	zones map[uuid.UUID]string
	down  map[uuid.UUID]bool
	srv   *httptest.Server
}

var (
	gatewayOnce sync.Once
	fake        *Gateway
)

// FakeGateway returns the shared fake gateway. An owner it has no zone for
// hasn't set one: UTC, saved false.
func FakeGateway(t *testing.T) *Gateway {
	t.Helper()
	gatewayOnce.Do(func() {
		fake = &Gateway{zones: map[uuid.UUID]string{}, down: map[uuid.UUID]bool{}}
		fake.srv = httptest.NewServer(http.HandlerFunc(fake.serve))
	})
	return fake
}

// SetZone records tz as owner's timezone, as if they saved it at the gateway.
func (g *Gateway) SetZone(owner uuid.UUID, tz string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.zones[owner] = tz
}

// Fail makes lookups of owner answer 503 until called again with false.
func (g *Gateway) Fail(owner uuid.UUID, down bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.down[owner] = down
}

// Client is a gateway client holding the key the fake accepts.
func (g *Gateway) Client() *gateway.Client {
	return &gateway.Client{URL: g.srv.URL, APIKey: gatewayKey}
}

func (g *Gateway) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.Method != http.MethodGet || len(parts) != 4 || parts[0] != "api" || parts[1] != "users" || parts[3] != "timezone" {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+gatewayKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"valid credentials are required"}`))
		return
	}
	id, err := uuid.Parse(parts[2])
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"malformed user id"}`))
		return
	}
	g.mu.Lock()
	tz, saved := g.zones[id]
	down := g.down[id]
	g.mu.Unlock()
	if down {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"failed to read the timezone"}`))
		return
	}
	if !saved {
		tz = "UTC"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"user_id": id, "timezone": tz, "saved": saved})
}
