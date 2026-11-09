package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/auth"
)

func TestGetUserIDRequiresIdentity(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	if _, ok := GetUserID(rec, req); ok {
		t.Fatalf("GetUserID without identity: ok = true, want false")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}

	userID := uuid.New()
	req = req.WithContext(auth.WithUserID(req.Context(), userID, []string{"coreban:read"}))
	rec = httptest.NewRecorder()
	got, ok := GetUserID(rec, req)
	if !ok || got != userID {
		t.Errorf("GetUserID = %v/%v, want %v/true", got, ok, userID)
	}
}

func TestResolveTargetUser(t *testing.T) {
	userID := uuid.New()
	target := uuid.New()

	newReq := func(scopes []string, actAs *uuid.UUID) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := auth.WithUserID(req.Context(), userID, scopes)
		if actAs != nil {
			ctx = auth.WithActAs(ctx, *actAs)
		}
		return req.WithContext(ctx)
	}

	rec := httptest.NewRecorder()
	got, ok := ResolveTargetUser(rec, newReq([]string{"admin"}, &target))
	if !ok || got != target {
		t.Errorf("admin+act_as = %v/%v, want %v/true", got, ok, target)
	}

	rec = httptest.NewRecorder()
	got, ok = ResolveTargetUser(rec, newReq([]string{"coreban:read"}, &target))
	if !ok || got != userID {
		t.Errorf("non-admin+act_as = %v/%v, want self %v/true", got, ok, userID)
	}

	rec = httptest.NewRecorder()
	got, ok = ResolveTargetUser(rec, newReq([]string{"admin"}, nil))
	if !ok || got != userID {
		t.Errorf("admin without act_as = %v/%v, want self %v/true", got, ok, userID)
	}
}
