package auth

import (
	"context"

	"github.com/google/uuid"
)

type contextKey string

const (
	userIDKey contextKey = "coreban-user-id"
	scopesKey contextKey = "coreban-scopes"
	actAsKey  contextKey = "coreban-act-as"
)

// WithUserID stores the verified user identity and scopes in the context.
// Only the verifier middleware may call it.
func WithUserID(ctx context.Context, userID uuid.UUID, scopes []string) context.Context {
	ctx = context.WithValue(ctx, userIDKey, userID)
	return context.WithValue(ctx, scopesKey, scopes)
}

// UserIDFromContext returns the verified user ID, or false when no identity
// was established.
func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userIDKey).(uuid.UUID)
	return id, ok
}

// ScopesFromContext returns the verified scope list, or nil when absent.
func ScopesFromContext(ctx context.Context) []string {
	scopes, _ := ctx.Value(scopesKey).([]string)
	return scopes
}

// WithActAs stores the gateway-signed delegation target. Only the verifier
// middleware may call it, and only with a parsed act_as claim.
func WithActAs(ctx context.Context, target uuid.UUID) context.Context {
	return context.WithValue(ctx, actAsKey, target)
}

// ActAsFromContext returns the delegation target, or false when absent.
func ActAsFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(actAsKey).(uuid.UUID)
	return id, ok
}

// HasScope reports whether the context carries the given scope.
func HasScope(ctx context.Context, scope string) bool {
	for _, s := range ScopesFromContext(ctx) {
		if s == scope {
			return true
		}
	}
	return false
}
