package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/gateway"
)

const key = "cgw_live_test"

// fakeGateway answers GET /api/users/{id}/timezone with status and body,
// for the dispatcher's key only.
func fakeGateway(t *testing.T, status int, body string) *gateway.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/users/") || !strings.HasSuffix(r.URL.Path, "/timezone") {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"valid credentials are required"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(strings.ReplaceAll(body, "{id}", strings.Split(r.URL.Path, "/")[3])))
	}))
	t.Cleanup(srv.Close)
	return &gateway.Client{URL: srv.URL, APIKey: key}
}

func TestASavedTimezoneIsTheOwnersZone(t *testing.T) {
	c := fakeGateway(t, http.StatusOK, `{"user_id":"{id}","timezone":"Asia/Manila","saved":true}`)
	loc, err := c.Timezone(context.Background(), uuid.New())
	if err != nil || loc.String() != "Asia/Manila" {
		t.Fatalf("Timezone = %v, %v; want Asia/Manila", loc, err)
	}
}

func TestAnUnsetTimezoneIsUTC(t *testing.T) {
	c := fakeGateway(t, http.StatusOK, `{"user_id":"{id}","timezone":"UTC","saved":false}`)
	loc, err := c.Timezone(context.Background(), uuid.New())
	if err != nil || loc.String() != "UTC" {
		t.Fatalf("Timezone = %v, %v; want UTC", loc, err)
	}
}

func TestAnythingButA200WithAKnownZoneIsAFailedLookup(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"forbidden", http.StatusForbidden, `{"error":"not allowed"}`, "403"},
		{"no such user", http.StatusNotFound, `{"error":"user not found"}`, "user not found"},
		{"store down", http.StatusServiceUnavailable, `{"error":"try again"}`, "503"},
		{"unknown zone", http.StatusOK, `{"user_id":"{id}","timezone":"Mars/Olympus_Mons","saved":true}`, "Mars/Olympus_Mons"},
		{"empty zone", http.StatusOK, `{"user_id":"{id}","timezone":"","saved":true}`, "timezone"},
		{"not json", http.StatusOK, `<html>`, "decode"},
	} {
		t.Run(c.name, func(t *testing.T) {
			loc, err := fakeGateway(t, c.status, c.body).Timezone(context.Background(), uuid.New())
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Timezone = %v, %v; want an error mentioning %q", loc, err, c.want)
			}
		})
	}
}

func TestAWrongKeyIsAFailedLookup(t *testing.T) {
	c := fakeGateway(t, http.StatusOK, `{"user_id":"{id}","timezone":"UTC","saved":false}`)
	c.APIKey = "cgw_live_revoked"
	if _, err := c.Timezone(context.Background(), uuid.New()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("Timezone with a wrong key: err = %v, want a 401 failure", err)
	}
}

func TestAnUnreachableGatewayIsAFailedLookup(t *testing.T) {
	c := &gateway.Client{URL: "http://127.0.0.1:1", APIKey: key}
	if _, err := c.Timezone(context.Background(), uuid.New()); err == nil {
		t.Error("Timezone with the gateway down: err = nil, want a failure")
	}
}
