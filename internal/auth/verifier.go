package auth

// Verifier checks gateway-minted internal JWTs against the gateway JWKS.
//
// This is a verbatim-behavior copy of the downstream verifier used by the
// other services in this repo (see the gateway's mirror test for the drift
// contract): short-lived Ed25519 tokens, expected issuer and audience,
// 60s clock-skew leeway, 5-minute JWKS cache with singleflight-coalesced
// refresh, 30s negative cache for unknown kids, fail-closed boot.

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

const (
	clockSkewLeeway    = 60 * time.Second
	defaultCacheTTL    = 5 * time.Minute
	negativeCacheTTL   = 30 * time.Second
	jwksRequestTimeout = 10 * time.Second
)

// Claims is the internal JWT shape minted by the gateway.
type Claims struct {
	jwt.RegisteredClaims
	Azp   string `json:"azp"`
	Scope string `json:"scope,omitempty"`
	ActAs string `json:"act_as,omitempty"`
}

// Config configures a Verifier.
type Config struct {
	Issuer      string
	Audience    string
	JWKSURL     string
	CacheTTL    time.Duration
	HTTPClient  *http.Client
	PublicPaths []string
}

// Verifier verifies internal JWTs. Use NewVerifier; the zero value is not usable.
type Verifier struct {
	cfg       Config
	mu        sync.RWMutex
	keys      map[string]ed25519.PublicKey
	fetchedAt time.Time
	// etag is the validator from the last 200 response; empty before
	// the first fetch. Guarded by mu.
	etag     string
	public   map[string]bool
	negative map[string]time.Time
	sf       singleflight.Group
}

// NewVerifier performs the initial JWKS fetch and fails closed without keys.
func NewVerifier(cfg Config) (*Verifier, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("auth: JWT issuer is required")
	}
	if cfg.Audience == "" {
		return nil, fmt.Errorf("auth: JWT audience is required")
	}
	if cfg.JWKSURL == "" {
		return nil, fmt.Errorf("auth: JWKS URL is required")
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = defaultCacheTTL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: jwksRequestTimeout}
	}
	v := &Verifier{
		cfg:      cfg,
		keys:     map[string]ed25519.PublicKey{},
		public:   map[string]bool{},
		negative: map[string]time.Time{},
	}
	for _, p := range cfg.PublicPaths {
		v.public[p] = true
	}
	if err := v.refresh(); err != nil {
		return nil, fmt.Errorf("auth: initial JWKS fetch: %w", err)
	}
	return v, nil
}

type jwksDocument struct {
	Keys []struct {
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		Kid string `json:"kid"`
		X   string `json:"x"`
		Use string `json:"use"`
		Alg string `json:"alg"`
	} `json:"keys"`
}

// fetch retrieves the JWKS. After the first success it sends the stored
// ETag, so an unchanged key set costs a 304 and no body; notModified then
// tells the caller to keep the keys it already has. A 304 with no stored
// validator cannot come from a compliant gateway and is an error, not an
// empty key set.
func (v *Verifier) fetch() (keys map[string]ed25519.PublicKey, etag string, notModified bool, err error) {
	req, err := http.NewRequest(http.MethodGet, v.cfg.JWKSURL, nil)
	if err != nil {
		return nil, "", false, err
	}
	v.mu.RLock()
	stored := v.etag
	v.mu.RUnlock()
	if stored != "" {
		req.Header.Set("If-None-Match", stored)
	}

	resp, err := v.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		if stored == "" {
			return nil, "", false, fmt.Errorf("JWKS 304 without a cached validator")
		}
		return nil, stored, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("JWKS status %d", resp.StatusCode)
	}

	var doc jwksDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, "", false, err
	}
	keys = make(map[string]ed25519.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" || k.Kid == "" {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		keys[k.Kid] = ed25519.PublicKey(raw)
	}
	if len(keys) == 0 {
		return nil, "", false, fmt.Errorf("JWKS contains no usable Ed25519 keys")
	}
	return keys, resp.Header.Get("ETag"), false, nil
}

func (v *Verifier) refresh() error {
	keys, etag, notModified, err := v.fetch()
	if err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !notModified {
		v.keys = keys
	}
	if etag != "" {
		v.etag = etag
	}
	v.fetchedAt = time.Now()
	return nil
}

func (v *Verifier) keyFor(kid string) (ed25519.PublicKey, bool) {
	v.mu.RLock()
	key, ok := v.keys[kid]
	fresh := time.Since(v.fetchedAt) < v.cfg.CacheTTL
	if ok && fresh {
		v.mu.RUnlock()
		return key, true
	}
	if until, neg := v.negative[kid]; neg && time.Now().Before(until) {
		v.mu.RUnlock()
		return nil, false
	}
	v.mu.RUnlock()

	_, err, _ := v.sf.Do("jwks-refresh", func() (any, error) {
		return nil, v.refresh()
	})
	if err != nil {
		return nil, false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	key, ok = v.keys[kid]
	if !ok {
		now := time.Now()
		for k, until := range v.negative {
			if !now.Before(until) {
				delete(v.negative, k)
			}
		}
		v.negative[kid] = now.Add(negativeCacheTTL)
		return nil, false
	}
	delete(v.negative, kid)
	return key, true
}

// Middleware verifies the internal JWT, strips any client-supplied
// X-User-ID, and injects the verified identity into the request context.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("X-User-ID")

		if v.public[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		presented, ok := bearerToken(r)
		if !ok {
			unauthorized(w)
			return
		}

		var claims Claims
		parsed, err := jwt.ParseWithClaims(presented, &claims,
			func(t *jwt.Token) (any, error) {
				if t.Method.Alg() != jwt.SigningMethodEdDSA.Alg() {
					return nil, jwt.ErrTokenSignatureInvalid
				}
				kid, _ := t.Header["kid"].(string)
				if kid == "" {
					return nil, jwt.ErrTokenUnverifiable
				}
				key, ok := v.keyFor(kid)
				if !ok {
					return nil, jwt.ErrTokenUnverifiable
				}
				return key, nil
			},
			jwt.WithIssuer(v.cfg.Issuer),
			jwt.WithAudience(v.cfg.Audience),
			jwt.WithLeeway(clockSkewLeeway),
			jwt.WithExpirationRequired(),
		)
		if err != nil || !parsed.Valid {
			unauthorized(w)
			return
		}

		if claims.Subject != "" {
			userID, err := uuid.Parse(claims.Subject)
			if err != nil {
				unauthorized(w)
				return
			}
			ctx := WithUserID(r.Context(), userID, strings.Fields(claims.Scope))
			if claims.ActAs != "" {
				if target, err := uuid.Parse(claims.ActAs); err == nil {
					ctx = WithActAs(ctx, target)
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// No service tokens exist (no minter): a token without a subject
		// satisfies no endpoint.
		unauthorized(w)
	})
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"valid internal credentials are required"}`))
}
