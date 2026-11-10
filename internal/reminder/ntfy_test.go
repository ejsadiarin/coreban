package reminder_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ejsadiarin/coreban/internal/reminder"
)

// fakeNtfy records what reaches it and answers each request with the next
// of statuses (200 once they run out).
type fakeNtfy struct {
	mu       sync.Mutex
	statuses []int
	got      []*http.Request
	bodies   []string
}

func (f *fakeNtfy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, r)
	f.bodies = append(f.bodies, string(body))
	status := http.StatusOK
	if len(f.statuses) > 0 {
		status, f.statuses = f.statuses[0], f.statuses[1:]
	}
	w.WriteHeader(status)
}

func ntfyServer(t *testing.T, statuses ...int) (*fakeNtfy, *reminder.Ntfy) {
	f := &fakeNtfy{statuses: statuses}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, &reminder.Ntfy{URL: srv.URL + "/", Token: "tk_secret", Backoff: time.Millisecond}
}

func TestNtfyPostsTheMessageToTheTopicWithCorebansToken(t *testing.T) {
	f, n := ntfyServer(t)
	at := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	if err := n.Notify(context.Background(), reminder.Notification{Topic: "coreban-ej", Message: "renew the cert", At: at}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(f.got) != 1 {
		t.Fatalf("requests = %d, want 1", len(f.got))
	}
	r := f.got[0]
	if r.Method != http.MethodPost || r.URL.Path != "/coreban-ej" {
		t.Errorf("request = %s %s, want POST /coreban-ej", r.Method, r.URL.Path)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer tk_secret" {
		t.Errorf("authorization = %q", got)
	}
	if f.bodies[0] != "renew the cert" || r.Header.Get("Title") != "Reminder" {
		t.Errorf("title %q body %q, want Reminder / the message", r.Header.Get("Title"), f.bodies[0])
	}
}

func TestNtfyMarksALateReminderWithItsMoment(t *testing.T) {
	f, n := ntfyServer(t)
	manila, _ := time.LoadLocation("Asia/Manila")
	at := time.Date(2026, 11, 10, 7, 0, 0, 0, manila)
	if err := n.Notify(context.Background(), reminder.Notification{Topic: "coreban-ej", Message: "standup", At: at, Late: true}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if got := f.got[0].Header.Get("Title"); got != "Late reminder" {
		t.Errorf("title = %q, want Late reminder", got)
	}
	// The IANA zone, not "PST": Manila shares that abbreviation with US Pacific.
	if want := "standup\n\nIt was set for Tue 10 Nov 07:00 (Asia/Manila)."; f.bodies[0] != want {
		t.Errorf("body = %q, want %q", f.bodies[0], want)
	}
}

func TestNtfyRetriesAServerErrorButNotARefusal(t *testing.T) {
	f, n := ntfyServer(t, http.StatusBadGateway, http.StatusOK)
	if err := n.Notify(context.Background(), reminder.Notification{Topic: "coreban-ej", Message: "x"}); err != nil {
		t.Fatalf("notify after one 502: %v", err)
	}
	if len(f.got) != 2 {
		t.Errorf("requests = %d, want a retry after the 502", len(f.got))
	}

	// Still failing after every try: worth trying again later.
	_, n = ntfyServer(t, http.StatusBadGateway, http.StatusBadGateway, http.StatusBadGateway)
	if err := n.Notify(context.Background(), reminder.Notification{Topic: "coreban-ej", Message: "x"}); !errors.Is(err, reminder.ErrRetryLater) {
		t.Errorf("error after three 502s = %v, want ErrRetryLater", err)
	}

	f, n = ntfyServer(t, http.StatusForbidden)
	err := n.Notify(context.Background(), reminder.Notification{Topic: "coreban-ej", Message: "x"})
	if err == nil || err.Error() != "ntfy answered 403 Forbidden" || errors.Is(err, reminder.ErrRetryLater) {
		t.Errorf("error = %v, want a final ntfy answered 403 Forbidden", err)
	}
	if len(f.got) != 1 {
		t.Errorf("requests = %d, want no retry after a 403", len(f.got))
	}
	if strings.Contains(err.Error(), "tk_secret") {
		t.Error("the error leaks the token")
	}
}
