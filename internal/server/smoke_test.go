package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

// The smoke test drives the real router against the harness Postgres with
// an injected identity: the path every later API slice's tests take.
func TestSmokeVerifiedHealthReachesPostgresAsTheOwner(t *testing.T) {
	owner := uuid.New()
	h := testharness.Router(t, testharness.Identity{Owner: owner, Scopes: []string{"coreban:read"}})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/coreban/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
	}
	var body struct{ Status, Owner string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Owner != owner.String() {
		t.Errorf("owner = %q, want %q", body.Owner, owner)
	}
}

func TestSmokeAdminTargetedReadResolvesToTheTargetUser(t *testing.T) {
	target := uuid.New()
	h := testharness.Router(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"admin"}, ActAs: target})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/coreban/health", nil))
	var body struct{ Owner string }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusOK || body.Owner != target.String() {
		t.Errorf("status/owner = %d/%q, want 200/%q", rec.Code, body.Owner, target)
	}
}

func TestSmokeNoInjectedIdentityIs401(t *testing.T) {
	h := testharness.Router(t, testharness.Identity{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/coreban/health", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
