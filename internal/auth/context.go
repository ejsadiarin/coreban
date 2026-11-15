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
	tzKey     contextKey = "coreban-tz"
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

// WithTimezone stores the gateway-asserted IANA zone of the user the
// request is about (the token's tz claim). Only the verifier middleware may
// call it; "" means the token carried none.
func WithTimezone(ctx context.Context, tz string) context.Context {
	return context.WithValue(ctx, tzKey, tz)
}

// TimezoneFromContext returns the token's tz claim and the user it is
// about: the act_as target on a targeted read, else the caller. ok is false
// when the token carried no tz (minted by a gateway that doesn't yet).
func TimezoneFromContext(ctx context.Context) (tz string, about uuid.UUID, ok bool) {
	tz, _ = ctx.Value(tzKey).(string)
	if tz == "" {
		return "", uuid.Nil, false
	}
	about, ok = ActAsFromContext(ctx)
	if !ok {
		about, ok = UserIDFromContext(ctx)
	}
	return tz, about, ok
}
