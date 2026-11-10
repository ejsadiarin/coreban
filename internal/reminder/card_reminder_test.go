package reminder_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

// kanbanCard and kanbanBoard are the slice of the boards API these tests
// drive.
type kanbanCard struct {
	ID      string `json:"id"`
	BoardID string `json:"board_id"`
	Title   string `json:"title"`
}

type kanbanBoard struct {
	ID      string `json:"id"`
	Columns []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"columns"`
}

func (o owner) board() kanbanBoard {
	o.t.Helper()
	rec := o.do(http.MethodPost, "/api/coreban/boards", map[string]string{"name": "Homelab"})
	expect(o.t, rec, http.StatusCreated)
	return decode[kanbanBoard](o.t, rec)
}

func (o owner) card(b kanbanBoard, title string) kanbanCard {
	o.t.Helper()
	rec := o.do(http.MethodPost, "/api/coreban/boards/"+b.ID+"/cards", map[string]string{"column_id": b.Columns[0].ID, "title": title})
	expect(o.t, rec, http.StatusCreated)
	return decode[kanbanCard](o.t, rec)
}

func cardURL(k kanbanCard) string { return "/api/coreban/boards/" + k.BoardID + "/cards/" + k.ID }

func (o owner) patchCard(k kanbanCard, body map[string]any) {
	o.t.Helper()
	expect(o.t, o.do(http.MethodPatch, cardURL(k), body), http.StatusOK)
}

// moveTo moves the card to the board's column of status.
func (o owner) moveTo(b kanbanBoard, k kanbanCard, status string) {
	o.t.Helper()
	for _, c := range b.Columns {
		if c.Status == status {
			expect(o.t, o.do(http.MethodPost, cardURL(k)+"/move", map[string]any{"column_id": c.ID, "index": 0}), http.StatusOK)
			return
		}
	}
	o.t.Fatalf("no %s column", status)
}

func (o owner) createOnCard(k kanbanCard, message string, sched map[string]any) item {
	o.t.Helper()
	rec := o.do(http.MethodPost, reminders, map[string]any{"message": message, "schedule": sched, "card_id": k.ID})
	expect(o.t, rec, http.StatusCreated)
	return decode[item](o.t, rec)
}

func TestAReminderAttachesToACardAndShowsWhichCard(t *testing.T) {
	o, _ := newOwner(t)
	b := o.board()
	k := o.card(b, "Renew the cert")

	// Without a message, it is the card's title.
	r := o.createOnCard(k, "", once(wall(future(time.UTC, time.Hour))))
	if r.Message != "Renew the cert" || r.Card == nil || r.Card.ID != k.ID || r.Card.BoardID != b.ID || r.Card.Title != "Renew the cert" {
		t.Errorf("reminder = %+v card %+v, want it on the card, titled after it", r, r.Card)
	}
	o.create("standalone", wall(future(time.UTC, 2*time.Hour)))

	rec := o.do(http.MethodGet, reminders+"?card_id="+k.ID, nil)
	expect(t, rec, http.StatusOK)
	if got := decode[[]item](t, rec); len(got) != 1 || got[0].ID != r.ID {
		t.Errorf("card's reminders = %+v, want just the one on it", got)
	}

	// Detach, then attach again.
	rec = o.do(http.MethodPatch, reminders+"/"+r.ID, map[string]any{"card_id": nil})
	expect(t, rec, http.StatusOK)
	if decode[item](t, rec).Card != nil {
		t.Error("detached reminder still has a card")
	}
	rec = o.do(http.MethodPatch, reminders+"/"+r.ID, map[string]any{"card_id": k.ID})
	expect(t, rec, http.StatusOK)
	if decode[item](t, rec).Card == nil {
		t.Error("re-attached reminder has no card")
	}
}

func TestOnlyYourOwnCardsTakeReminders(t *testing.T) {
	alice, _ := newOwner(t)
	theirs := alice.card(alice.board(), "Alice's")
	bob, _ := newOwner(t)
	for _, body := range []map[string]any{
		{"message": "x", "schedule": once(wall(future(time.UTC, time.Hour))), "card_id": theirs.ID},
		{"message": "x", "schedule": once(wall(future(time.UTC, time.Hour))), "card_id": "not-a-uuid"},
	} {
		rec := bob.do(http.MethodPost, reminders, body)
		expect(t, rec, http.StatusBadRequest)
		if got := decode[map[string]string](t, rec)["error"]; got != "card_id is not one of your cards" {
			t.Errorf("error = %q", got)
		}
	}
}

func TestRemindMeAtDueUsesTheCardsDue(t *testing.T) {
	o, _ := newOwner(t)
	o.setTarget(nil, "Asia/Manila")
	b := o.board()
	k := o.card(b, "Ship the release")

	refuse := func(want string) {
		t.Helper()
		rec := o.do(http.MethodPost, reminders, map[string]any{"schedule": map[string]any{"kind": "due"}, "card_id": k.ID})
		expect(t, rec, http.StatusBadRequest)
		if got := decode[map[string]string](t, rec)["error"]; got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
	}
	refuse("the card has no due")

	at := future(mustLoad(t, "Asia/Manila"), 72*time.Hour)
	o.patchCard(k, map[string]any{"due": map[string]string{"at": wall(at)}})
	r := o.createOnCard(k, "", map[string]any{"kind": "due"})
	if r.Schedule.Kind != "once" || r.Schedule.At != at.Format(time.RFC3339) || r.Message != "Ship the release" {
		t.Errorf("at-due reminder = %+v, want once at %s", r, at.Format(time.RFC3339))
	}

	// A date-only due is reminded at 09:00 in the owner's timezone.
	day := time.Now().AddDate(0, 0, 5).Format("2006-01-02")
	o.patchCard(k, map[string]any{"due": map[string]string{"date": day}})
	r = o.createOnCard(k, "", map[string]any{"kind": "due"})
	if want := day + "T09:00:00+08:00"; r.Schedule.At != want {
		t.Errorf("date due reminder at %s, want %s", r.Schedule.At, want)
	}

	o.patchCard(k, map[string]any{"due": map[string]string{"date": "2020-01-01"}})
	refuse("the card's due has passed")
	rec := o.do(http.MethodPost, reminders, map[string]any{"schedule": map[string]any{"kind": "due"}})
	expect(t, rec, http.StatusBadRequest)
	if got := decode[map[string]string](t, rec)["error"]; got != `schedule.kind "due" needs a card_id` {
		t.Errorf("due without a card: error = %q", got)
	}
}

func TestACompletedOrArchivedCardsRemindersStayQuietAndResume(t *testing.T) {
	o, topic := subscribed(t, "Asia/Manila")
	manila := mustLoad(t, "Asia/Manila")
	b := o.board()
	k := o.card(b, "Daily standup notes")
	r := o.createOnCard(k, "", daily("07:00"))
	n := &inbox{}
	ctx := context.Background()
	day := func(d int) time.Time { return time.Date(2027, 6, d, 7, 0, 0, 0, manila) }

	tickAt(t, ctx, n, day(1))
	o.moveTo(b, k, "done")
	if p := o.list()[0].Paused; p == nil || *p != "card_completed" {
		t.Errorf("paused = %v, want card_completed", p)
	}
	tickAt(t, ctx, n, day(2))
	o.moveTo(b, k, "in_progress") // reopened
	tickAt(t, ctx, n, day(3))
	o.patchCard(k, map[string]any{"archived": true})
	if p := o.list()[0].Paused; p == nil || *p != "card_archived" {
		t.Errorf("paused = %v, want card_archived", p)
	}
	tickAt(t, ctx, n, day(4))
	o.patchCard(k, map[string]any{"archived": false})
	tickAt(t, ctx, n, day(5))

	want := []string{"Tue 2027-06-01 07:00 PST", "Thu 2027-06-03 07:00 PST", "Sat 2027-06-05 07:00 PST"}
	if got := sends(n, topic, manila); !slices.Equal(got, want) {
		t.Errorf("sends = %v, want %v (quiet on the 2nd and 4th)", got, want)
	}
	// Silencing never deleted it.
	expect(t, o.do(http.MethodGet, reminders+"/"+r.ID, nil), http.StatusOK)
}

func TestAOneOffOnACompletedCardIsSkippedNotSentLater(t *testing.T) {
	o, topic := subscribed(t, "UTC")
	b := o.board()
	k := o.card(b, "Done already")
	at := future(time.UTC, 2*time.Hour)
	r := o.createOnCard(k, "", once(wall(at)))
	o.moveTo(b, k, "done")
	n := &inbox{}
	tickAt(t, context.Background(), n, at)
	o.moveTo(b, k, "todo")
	tickAt(t, context.Background(), n, at.Add(time.Hour))
	if got := n.to(topic); len(got) != 0 {
		t.Errorf("sent %+v, want the skipped one-off never sent", got)
	}
	expect(t, o.do(http.MethodGet, reminders+"/"+r.ID, nil), http.StatusOK)
}

func TestDeletingACardDeletesItsReminders(t *testing.T) {
	o, _ := newOwner(t)
	k := o.card(o.board(), "Temporary")
	r := o.createOnCard(k, "", daily("07:00"))
	expect(t, o.do(http.MethodDelete, cardURL(k), nil), http.StatusNoContent)
	expect(t, o.do(http.MethodGet, reminders+"/"+r.ID, nil), http.StatusNotFound)
}

func TestAReminderCannotBeMovedOntoSomeoneElsesCard(t *testing.T) {
	alice, aliceID := newOwner(t)
	theirs := alice.card(alice.board(), "Alice's")
	bob, _ := newOwner(t)
	r := bob.create("mine", wall(future(time.UTC, time.Hour)))
	rec := bob.do(http.MethodPatch, reminders+"/"+r.ID, map[string]any{"card_id": theirs.ID})
	expect(t, rec, http.StatusBadRequest)
	if got := decode[map[string]string](t, rec)["error"]; got != "card_id is not one of your cards" {
		t.Errorf("error = %q", got)
	}
	// A targeted read can't write through to the target's cards either.
	admin := as(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"admin"}, ActAs: aliceID})
	rec = admin.do(http.MethodPost, reminders, map[string]any{"message": "x", "schedule": once(wall(future(time.UTC, time.Hour))), "card_id": theirs.ID})
	expect(t, rec, http.StatusBadRequest)
}
