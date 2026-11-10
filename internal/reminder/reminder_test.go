package reminder_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

type schedule struct {
	Kind string `json:"kind"`
	At   string `json:"at"`
}

type delivery struct {
	At    string  `json:"at"`
	Late  bool    `json:"late"`
	Error *string `json:"error"`
}

type item struct {
	ID           string    `json:"id"`
	Message      string    `json:"message"`
	Schedule     schedule  `json:"schedule"`
	NextFireAt   *string   `json:"next_fire_at"`
	Paused       *string   `json:"paused"`
	LastDelivery *delivery `json:"last_delivery"`
}

// owner is one caller driving the API with a fixed injected identity.
type owner struct {
	t *testing.T
	h http.Handler
}

func as(t *testing.T, id testharness.Identity) owner {
	return owner{t: t, h: testharness.Router(t, id)}
}

func identity(id uuid.UUID) testharness.Identity {
	return testharness.Identity{Owner: id, Scopes: []string{"coreban:read", "coreban:write"}}
}

func newOwner(t *testing.T) (owner, uuid.UUID) {
	id := uuid.New()
	return as(t, identity(id)), id
}

func (o owner) do(method, path string, body any) *httptest.ResponseRecorder {
	o.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			o.t.Fatalf("encode body: %v", err)
		}
	}
	rec := httptest.NewRecorder()
	o.h.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body)
	}
}

const reminders = "/api/coreban/reminders"

func once(at string) map[string]any {
	return map[string]any{"kind": "once", "at": at}
}

// setTarget saves the owner's notification target and timezone.
func (o owner) setTarget(topic any, tz string) {
	o.t.Helper()
	expect(o.t, o.do(http.MethodPut, "/api/coreban/settings", map[string]any{"ntfy_topic": topic, "timezone": tz}), http.StatusOK)
}

func (o owner) create(message string, at string) item {
	o.t.Helper()
	rec := o.do(http.MethodPost, reminders, map[string]any{"message": message, "schedule": once(at)})
	expect(o.t, rec, http.StatusCreated)
	return decode[item](o.t, rec)
}

func (o owner) list() []item {
	o.t.Helper()
	rec := o.do(http.MethodGet, reminders, nil)
	expect(o.t, rec, http.StatusOK)
	return decode[[]item](o.t, rec)
}

// future is a wall-clock moment safely after now, in loc, without an offset
// (the form the web sends).
func future(loc *time.Location, d time.Duration) time.Time {
	return time.Now().Add(d).In(loc).Truncate(time.Minute)
}

func wall(t time.Time) string { return t.Format("2006-01-02T15:04") }

func TestAOneOffIsListedWithItsNextFiringInTheOwnersTimezone(t *testing.T) {
	o, _ := newOwner(t)
	o.setTarget(nil, "Asia/Manila")
	manila, _ := time.LoadLocation("Asia/Manila")
	at := future(manila, 48*time.Hour)

	created := o.create("  renew the cert  ", wall(at))
	if created.Message != "renew the cert" {
		t.Errorf("message = %q, want trimmed", created.Message)
	}

	got := o.list()
	if len(got) != 1 || got[0].ID != created.ID {
		t.Fatalf("list = %+v, want the one created", got)
	}
	r := got[0]
	want := at.Format(time.RFC3339) // +08:00: the owner's own zone
	if r.Schedule.Kind != "once" || r.Schedule.At != want {
		t.Errorf("schedule = %+v, want once at %s", r.Schedule, want)
	}
	if r.NextFireAt == nil || *r.NextFireAt != want {
		t.Errorf("next_fire_at = %v, want %s", r.NextFireAt, want)
	}
	if r.LastDelivery != nil {
		t.Errorf("last_delivery = %+v, want none yet", r.LastDelivery)
	}
}

func TestAnInstantWithAnOffsetIsKeptAsThatInstant(t *testing.T) {
	o, _ := newOwner(t)
	o.setTarget(nil, "Asia/Manila")
	at := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)

	r := o.create("ship it", at.Format(time.RFC3339))
	got, err := time.Parse(time.RFC3339, r.Schedule.At)
	if err != nil || !got.Equal(at) {
		t.Fatalf("at = %s, want the instant %s", r.Schedule.At, at.Format(time.RFC3339))
	}
	if _, off := got.Zone(); off != 8*3600 {
		t.Errorf("at = %s, want it shown in the owner's zone (+08:00)", r.Schedule.At)
	}
}

func TestEditingChangesTheMessageAndANewMomentRearmsIt(t *testing.T) {
	o, _ := newOwner(t)
	r := o.create("water the plants", wall(future(time.UTC, 24*time.Hour)))
	path := reminders + "/" + r.ID

	rec := o.do(http.MethodPatch, path, map[string]any{"message": "water the ferns"})
	expect(t, rec, http.StatusOK)
	if got := decode[item](t, rec); got.Message != "water the ferns" || got.Schedule != r.Schedule {
		t.Errorf("after message edit: %+v, want new message, same schedule %+v", got, r.Schedule)
	}

	later := future(time.UTC, 72*time.Hour)
	rec = o.do(http.MethodPatch, path, map[string]any{"schedule": once(wall(later))})
	expect(t, rec, http.StatusOK)
	got := decode[item](t, rec)
	want := later.Format(time.RFC3339)
	if got.Schedule.At != want || got.NextFireAt == nil || *got.NextFireAt != want {
		t.Errorf("after reschedule: at %s next %v, want both %s", got.Schedule.At, got.NextFireAt, want)
	}
	if got.Message != "water the ferns" {
		t.Errorf("message = %q, want it kept", got.Message)
	}
}

func TestDeletingARemindersRemovesItForGood(t *testing.T) {
	o, _ := newOwner(t)
	r := o.create("one", wall(future(time.UTC, time.Hour)))
	path := reminders + "/" + r.ID
	expect(t, o.do(http.MethodDelete, path, nil), http.StatusNoContent)
	expect(t, o.do(http.MethodGet, path, nil), http.StatusNotFound)
	expect(t, o.do(http.MethodDelete, path, nil), http.StatusNotFound)
	if got := o.list(); len(got) != 0 {
		t.Errorf("list = %+v, want empty", got)
	}
}

func TestRemindersAreRejectedWithAReasonTheOwnerCanFix(t *testing.T) {
	o, _ := newOwner(t)
	soon := wall(future(time.UTC, time.Hour))
	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"blank message", map[string]any{"message": "  ", "schedule": once(soon)}, "message is required"},
		{"long message", map[string]any{"message": strings.Repeat("é", 1001), "schedule": once(soon)}, "message must be at most 1000 characters"},
		{"no schedule", map[string]any{"message": "x"}, `schedule.kind must be "once"`},
		{"unknown kind", map[string]any{"message": "x", "schedule": map[string]any{"kind": "hourly", "at": soon}}, `schedule.kind must be "once"`},
		{"not a moment", map[string]any{"message": "x", "schedule": once("tomorrow")}, "schedule.at must be a date and time such as 2026-11-10T07:00"},
		{"in the past", map[string]any{"message": "x", "schedule": once("2020-01-01T07:00")}, "schedule.at must be in the future"},
	}
	for _, c := range cases {
		rec := o.do(http.MethodPost, reminders, c.body)
		expect(t, rec, http.StatusBadRequest)
		if got := decode[map[string]string](t, rec)["error"]; got != c.want {
			t.Errorf("%s: error = %q, want %q", c.name, got, c.want)
		}
	}
	r := o.create("fine", soon)
	rec := o.do(http.MethodPatch, reminders+"/"+r.ID, map[string]any{"schedule": once("2020-01-01T07:00")})
	expect(t, rec, http.StatusBadRequest)
	if got := o.list(); len(got) != 1 || got[0].Schedule != r.Schedule {
		t.Errorf("a rejected edit changed the reminder: %+v", got)
	}
}

func TestAnOwnersRemindersAreInvisibleToOthersExceptThroughATargetedRead(t *testing.T) {
	alice, aliceID := newOwner(t)
	r := alice.create("alice only", wall(future(time.UTC, time.Hour)))
	path := reminders + "/" + r.ID

	bob, _ := newOwner(t)
	if got := bob.list(); len(got) != 0 {
		t.Errorf("bob sees %+v", got)
	}
	expect(t, bob.do(http.MethodGet, path, nil), http.StatusNotFound)
	expect(t, bob.do(http.MethodPatch, path, map[string]any{"message": "mine"}), http.StatusNotFound)
	expect(t, bob.do(http.MethodDelete, path, nil), http.StatusNotFound)

	admin := as(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"admin"}, ActAs: aliceID})
	if got := admin.list(); len(got) != 1 || got[0].ID != r.ID {
		t.Errorf("targeted read = %+v, want alice's reminder", got)
	}
	// Writes bind to the caller: the admin creates their own, not alice's.
	admin.create("admin's own", wall(future(time.UTC, time.Hour)))
	if got := alice.list(); len(got) != 1 {
		t.Errorf("alice has %d reminders after an admin write, want 1", len(got))
	}
}

func TestAnOwnerCannotCreateMoreThanTheCap(t *testing.T) {
	id := uuid.New()
	s := testharness.Server(t, identity(id))
	s.Reminders.Cap = 2
	o := owner{t: t, h: s.Routes()}
	soon := wall(future(time.UTC, time.Hour))
	o.create("one", soon)
	second := o.create("two", soon)

	rec := o.do(http.MethodPost, reminders, map[string]any{"message": "three", "schedule": once(soon)})
	expect(t, rec, http.StatusConflict)
	want := "you already have 2 reminders, the most one owner may have; delete one first"
	if got := decode[map[string]string](t, rec)["error"]; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}

	expect(t, o.do(http.MethodDelete, reminders+"/"+second.ID, nil), http.StatusNoContent)
	o.create("three", soon)
}

func TestTheListSaysWhyNothingWillBeSentWithoutANotificationTarget(t *testing.T) {
	o, _ := newOwner(t)
	r := o.create("ping", wall(future(time.UTC, time.Hour)))
	if r.Paused == nil || *r.Paused != "no_notification_target" {
		t.Errorf("paused = %v, want no_notification_target", r.Paused)
	}
	o.setTarget("coreban-"+uuid.NewString()[:8], "UTC")
	if got := o.list(); got[0].Paused != nil {
		t.Errorf("paused = %v after setting a target, want none", *got[0].Paused)
	}
}
