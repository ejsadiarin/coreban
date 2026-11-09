package server_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/auth"
	"github.com/ejsadiarin/coreban/internal/server"
)

// verifiedRouter builds the production router behind a real verifier that
// trusts one freshly generated gateway key. The pool is nil: every request
// here is rejected (or served) before any handler touches the database.
func verifiedRouter(t *testing.T) (http.Handler, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k1","x":%q}]}`,
			base64.RawURLEncoding.EncodeToString(pub))
	}))
	t.Cleanup(jwks.Close)
	v, err := auth.NewVerifier(auth.Config{
		Issuer:   "https://gateway.internal",
		Audience: "coreban",
		JWKSURL:  jwks.URL,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return server.New(nil, v.Middleware).Routes(), priv
}

func gatewayToken(t *testing.T, priv ed25519.PrivateKey, audience string) string {
	t.Helper()
	claims := auth.Claims{}
	claims.Issuer = "https://gateway.internal"
	claims.Audience = jwt.ClaimStrings{audience}
	claims.Subject = uuid.New().String()
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(5 * time.Minute))
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = "k1"
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func TestHealthNeedsNoToken(t *testing.T) {
	h, _ := verifiedRouter(t)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/health", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s /health status = %d, want 200", method, rec.Code)
		}
	}
}

func TestAPIRejectsRequestsWithoutAValidCorebanToken(t *testing.T) {
	h, priv := verifiedRouter(t)
	cases := map[string]func(r *http.Request){
		"no token":            func(*http.Request) {},
		"user-ID header only": func(r *http.Request) { r.Header.Set("X-User-ID", uuid.NewString()) },
		"garbage bearer":      func(r *http.Request) { r.Header.Set("Authorization", "Bearer not-a-jwt") },
		"other service's aud": func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+gatewayToken(t, priv, "coredraw")) },
		"gateway API key":     func(r *http.Request) { r.Header.Set("Authorization", "Bearer cgw_live_abc") },
	}
	for name, prepare := range cases {
		for _, path := range []string{"/api/coreban/health", "/api/coreban/boards", "/api/coreban/anything/else"} {
			t.Run(name+" "+path, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				prepare(req)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusUnauthorized {
					t.Errorf("status = %d, want 401", rec.Code)
				}
			})
		}
	}
}
