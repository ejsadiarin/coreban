package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	testIssuer   = "https://gateway.internal"
	testAudience = "coreban"
)

type jwksHarness struct {
	t       *testing.T
	mu      sync.Mutex
	keys    map[string]ed25519.PublicKey
	fetches atomic.Int64
	fail    atomic.Bool
	server  *httptest.Server
}

func newJWKSHarness(t *testing.T, keys map[string]ed25519.PublicKey) *jwksHarness {
	t.Helper()
	h := &jwksHarness{t: t, keys: keys}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.fetches.Add(1)
		if h.fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		var b strings.Builder
		b.WriteString(`{"keys":[`)
		first := true
		for kid, pub := range h.keys {
			if !first {
				b.WriteString(",")
			}
			first = false
			fmt.Fprintf(&b, `{"kty":"OKP","crv":"Ed25519","kid":%q,"x":%q,"use":"sig","alg":"EdDSA"}`,
				kid, base64.RawURLEncoding.EncodeToString(pub))
		}
		b.WriteString(`]}`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(h.server.Close)
	return h
}

func (h *jwksHarness) publish(kid string, pub ed25519.PublicKey) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.keys[kid] = pub
}

func mustKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return priv
}

func signToken(t *testing.T, priv ed25519.PrivateKey, kid string, mutate func(*Claims)) string {
	t.Helper()
	claims := Claims{}
	claims.Issuer = testIssuer
	claims.Audience = jwt.ClaimStrings{testAudience}
	claims.Subject = uuid.New().String()
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(5 * time.Minute))
	claims.IssuedAt = jwt.NewNumericDate(time.Now())
	if mutate != nil {
		mutate(&claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(priv)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func newTestVerifier(t *testing.T, h *jwksHarness, mutateCfg func(*Config)) *Verifier {
	t.Helper()
	cfg := Config{
		Issuer:   testIssuer,
		Audience: testAudience,
		JWKSURL:  h.server.URL,
	}
	if mutateCfg != nil {
		mutateCfg(&cfg)
	}
	v, err := NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func serve(t *testing.T, v *Verifier, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	var reached atomic.Bool
	var gotUser uuid.UUID
	var gotSpoof string
	rec := httptest.NewRecorder()
	v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		gotUser, _ = UserIDFromContext(r.Context())
		gotSpoof = r.Header.Get("X-User-ID")
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, r)
	if rec.Code == http.StatusOK && !reached.Load() {
		t.Fatalf("handler not reached on 200")
	}
	_ = gotUser
	_ = gotSpoof
	return rec
}

func TestMiddlewareValidTokenReachesHandlerWithSpoofStripped(t *testing.T) {
	priv := mustKey(t)
	h := newJWKSHarness(t, map[string]ed25519.PublicKey{"2026-09-a": priv.Public().(ed25519.PublicKey)})
	v := newTestVerifier(t, h, nil)

	sub := uuid.New()
	token := signToken(t, priv, "2026-09-a", func(c *Claims) { c.Subject = sub.String() })
	req := httptest.NewRequest(http.MethodGet, "/api/coreban/boards", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-User-ID", "00000000-0000-0000-0000-000000000000")

	var gotUser uuid.UUID
	var gotSpoof string
	var reached bool
	rec := httptest.NewRecorder()
	v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		gotUser, _ = UserIDFromContext(r.Context())
		gotSpoof = r.Header.Get("X-User-ID")
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if !reached {
		t.Fatalf("handler not reached")
	}
	if gotUser != sub {
		t.Errorf("user = %v, want %v", gotUser, sub)
	}
	if gotSpoof != "" {
		t.Errorf("X-User-ID reached handler = %q, want stripped", gotSpoof)
	}
}

func TestMiddlewareMissingMalformedBearer(t *testing.T) {
	priv := mustKey(t)
	h := newJWKSHarness(t, map[string]ed25519.PublicKey{"2026-09-a": priv.Public().(ed25519.PublicKey)})
	v := newTestVerifier(t, h, nil)

	for _, header := range []string{"", "Bearer", "Bearer ", "Token abc"} {
		req := httptest.NewRequest(http.MethodGet, "/api/coreban/boards", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		var reached atomic.Bool
		rec := httptest.NewRecorder()
		v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached.Store(true)
		})).ServeHTTP(rec, req)
		if reached.Load() {
			t.Errorf("header %q: handler reached, want 401", header)
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("header %q: status = %d, want 401", header, rec.Code)
		}
		if strings.TrimSpace(rec.Body.String()) != `{"error":"valid internal credentials are required"}` {
			t.Errorf("header %q: body = %q", header, rec.Body.String())
		}
	}
}

func TestMiddlewareRejectsWrongKeyIssuerAudience(t *testing.T) {
	priv := mustKey(t)
	other := mustKey(t)
	h := newJWKSHarness(t, map[string]ed25519.PublicKey{"2026-09-a": priv.Public().(ed25519.PublicKey)})
	v := newTestVerifier(t, h, nil)

	cases := map[string]func() string{
		"wrong key": func() string {
			return signToken(t, other, "2026-09-a", nil)
		},
		"wrong iss": func() string {
			return signToken(t, priv, "2026-09-a", func(c *Claims) { c.Issuer = "https://rogue.internal" })
		},
		"wrong aud": func() string {
			return signToken(t, priv, "2026-09-a", func(c *Claims) { c.Audience = jwt.ClaimStrings{"corefinance"} })
		},
	}
	for name, makeToken := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/coreban/boards", nil)
		req.Header.Set("Authorization", "Bearer "+makeToken())
		rec := serve(t, v, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, rec.Code)
		}
	}
}

func TestMiddlewareExpiryLeeway(t *testing.T) {
	priv := mustKey(t)
	h := newJWKSHarness(t, map[string]ed25519.PublicKey{"2026-09-a": priv.Public().(ed25519.PublicKey)})
	v := newTestVerifier(t, h, nil)

	fresh := signToken(t, priv, "2026-09-a", func(c *Claims) {
		c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-30 * time.Second))
	})
	if rec := serve(t, v, bearerReq(fresh)); rec.Code != http.StatusOK {
		t.Errorf("30s-expired: status = %d, want 200 (60s leeway)", rec.Code)
	}

	stale := signToken(t, priv, "2026-09-a", func(c *Claims) {
		c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-61 * time.Second))
	})
	if rec := serve(t, v, bearerReq(stale)); rec.Code != http.StatusUnauthorized {
		t.Errorf("61s-expired: status = %d, want 401", rec.Code)
	}
}

func bearerReq(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/coreban/boards", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestMiddlewareNewKidVerifiesAfterRefresh(t *testing.T) {
	privA := mustKey(t)
	privB := mustKey(t)
	h := newJWKSHarness(t, map[string]ed25519.PublicKey{"kid-a": privA.Public().(ed25519.PublicKey)})
	v := newTestVerifier(t, h, nil)

	h.publish("kid-b", privB.Public().(ed25519.PublicKey))
	token := signToken(t, privB, "kid-b", nil)
	if rec := serve(t, v, bearerReq(token)); rec.Code != http.StatusOK {
		t.Errorf("new kid after refresh: status = %d, want 200", rec.Code)
	}
}

func TestMiddlewareForgedKidFetchesOnceThenNegativeCache(t *testing.T) {
	priv := mustKey(t)
	h := newJWKSHarness(t, map[string]ed25519.PublicKey{"2026-09-a": priv.Public().(ed25519.PublicKey)})
	v := newTestVerifier(t, h, nil)
	h.fetches.Store(0)

	forged := signToken(t, priv, "forged-kid", nil)
	for i := 0; i < 2; i++ {
		if rec := serve(t, v, bearerReq(forged)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i, rec.Code)
		}
	}
	if got := h.fetches.Load(); got != 1 {
		t.Errorf("refresh fetches = %d, want 1 (second rejection from negative cache)", got)
	}
}

func TestMiddlewareConcurrentStaleCoalescesToOneFetch(t *testing.T) {
	priv := mustKey(t)
	h := newJWKSHarness(t, map[string]ed25519.PublicKey{"2026-09-a": priv.Public().(ed25519.PublicKey)})
	v := newTestVerifier(t, h, func(c *Config) { c.CacheTTL = 30 * time.Millisecond })
	time.Sleep(60 * time.Millisecond)
	h.fetches.Store(0)

	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token := signToken(t, priv, "2026-09-a", nil)
			codes[i] = serve(t, v, bearerReq(token)).Code
		}(i)
	}
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("request %d: status = %d, want 200", i, code)
		}
	}
	if got := h.fetches.Load(); got != 1 {
		t.Errorf("refresh fetches = %d, want 1 (singleflight coalesced)", got)
	}
}

func TestMiddlewareRefreshFailureRetriesNextRequest(t *testing.T) {
	priv := mustKey(t)
	h := newJWKSHarness(t, map[string]ed25519.PublicKey{"2026-09-a": priv.Public().(ed25519.PublicKey)})
	v := newTestVerifier(t, h, func(c *Config) { c.CacheTTL = 30 * time.Millisecond })
	time.Sleep(60 * time.Millisecond)

	h.fail.Store(true)
	token := signToken(t, priv, "2026-09-a", nil)
	if rec := serve(t, v, bearerReq(token)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("failed refresh: status = %d, want 401", rec.Code)
	}

	h.fail.Store(false)
	if rec := serve(t, v, bearerReq(token)); rec.Code != http.StatusOK {
		t.Errorf("after recovery: status = %d, want 200 (no negative entry on failure)", rec.Code)
	}
}

func TestMiddlewareSubLessTokenRejected(t *testing.T) {
	priv := mustKey(t)
	h := newJWKSHarness(t, map[string]ed25519.PublicKey{"2026-09-a": priv.Public().(ed25519.PublicKey)})
	v := newTestVerifier(t, h, nil)

	token := signToken(t, priv, "2026-09-a", func(c *Claims) { c.Subject = "" })
	if rec := serve(t, v, bearerReq(token)); rec.Code != http.StatusUnauthorized {
		t.Errorf("sub-less: status = %d, want 401", rec.Code)
	}
}

func TestNewVerifierUnreachableJWKRFailsClosed(t *testing.T) {
	_, err := NewVerifier(Config{
		Issuer:   testIssuer,
		Audience: testAudience,
		JWKSURL:  "http://127.0.0.1:1/.well-known/jwks.json",
	})
	if err == nil {
		t.Fatalf("NewVerifier with unreachable JWKS: want error, got nil")
	}
}

func TestRefreshConditionalGet(t *testing.T) {
	priv := mustKey(t)
	kid := "2026-09-a"

	var mu sync.Mutex
	etag := `"v1"`
	keys := map[string]ed25519.PublicKey{kid: priv.Public().(ed25519.PublicKey)}
	notModified := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("If-None-Match") == etag {
			notModified++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		var b strings.Builder
		b.WriteString(`{"keys":[`)
		first := true
		for k, pub := range keys {
			if !first {
				b.WriteString(",")
			}
			first = false
			fmt.Fprintf(&b, `{"kty":"OKP","crv":"Ed25519","kid":%q,"x":%q,"use":"sig","alg":"EdDSA"}`,
				k, base64.RawURLEncoding.EncodeToString(pub))
		}
		b.WriteString(`]}`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(srv.Close)

	v, err := NewVerifier(Config{Issuer: testIssuer, Audience: testAudience, JWKSURL: srv.URL})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	// Unchanged key set: the refresh carries the validator and gets a 304.
	if err := v.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	mu.Lock()
	conditional := notModified
	mu.Unlock()
	if conditional != 1 {
		t.Fatalf("conditional requests = %d, want 1", conditional)
	}
	v.mu.RLock()
	cached, ok := v.keys[kid]
	v.mu.RUnlock()
	if !ok || !cached.Equal(priv.Public()) {
		t.Fatal("304 dropped the cached key")
	}

	// A new key set (new ETag) replaces the cache.
	newPriv := mustKey(t)
	mu.Lock()
	etag = `"v2"`
	keys = map[string]ed25519.PublicKey{"2026-09-b": newPriv.Public().(ed25519.PublicKey)}
	mu.Unlock()
	if err := v.refresh(); err != nil {
		t.Fatalf("refresh after rotation: %v", err)
	}
	v.mu.RLock()
	_, oldPresent := v.keys[kid]
	rotated, newPresent := v.keys["2026-09-b"]
	v.mu.RUnlock()
	if oldPresent {
		t.Error("retired kid still published after a changed key set")
	}
	if !newPresent || !rotated.Equal(newPriv.Public()) {
		t.Error("rotated key was not picked up")
	}
}
