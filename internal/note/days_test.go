package note_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

type noteDays struct {
	Days []string `json:"days"`
}

func (o owner) days(query string) noteDays {
	o.t.Helper()
	rec := o.do(http.MethodGet, "/api/coreban/notes/days"+query, nil)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("days %q: status = %d, body %s", query, rec.Code, rec.Body)
	}
	return decode[noteDays](o.t, rec)
}

func manilaOwner(t *testing.T) owner {
	return as(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"coreban:read", "coreban:write"}, Timezone: manila})
}

func TestTheDaysWithNotesAreCountedInTheOwnersZone(t *testing.T) {
	o := manilaOwner(t)
	o.sync(
		syncRecord{Kind: "expense", ID: uuid.NewString(), Date: "2026-11-05", PostedAt: "2026-11-05T09:00:00+08:00"},
		// 15:30 UTC: still Nov 7 in Manila.
		syncRecord{Kind: "income", ID: uuid.NewString(), Date: "2026-11-07", PostedAt: "2026-11-07T23:30:00+08:00"},
	)
	got := o.days("?from=2026-11-01&to=2026-11-30").Days
	if want := []string{"2026-11-05", "2026-11-07"}; !slices.Equal(got, want) {
		t.Fatalf("days = %v, want %v", got, want)
	}
}

func TestAnArchivedNoteLeavesItsDayUnmarked(t *testing.T) {
	o := manilaOwner(t)
	loc, err := time.LoadLocation(manila)
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(loc).Format("2006-01-02")
	n := o.post("a thought")
	q := "?from=" + today + "&to=" + today
	if got := o.days(q).Days; !slices.Equal(got, []string{today}) {
		t.Fatalf("with the note: days = %v, want [%s]", got, today)
	}
	if rec := o.do(http.MethodPatch, "/api/coreban/notes/"+n.ID, map[string]bool{"archived": true}); rec.Code != http.StatusOK {
		t.Fatalf("archive: status = %d, body %s", rec.Code, rec.Body)
	}
	if got := o.days(q).Days; len(got) != 0 {
		t.Fatalf("archived: days = %v, want none", got)
	}
}

func TestTheDaysAreOnlyYourOwn(t *testing.T) {
	o := manilaOwner(t)
	o.sync(syncRecord{Kind: "expense", ID: uuid.NewString(), Date: "2026-11-05", PostedAt: "2026-11-05T09:00:00+08:00"})
	if got := manilaOwner(t).days("?from=2026-11-01&to=2026-11-30").Days; len(got) != 0 {
		t.Fatalf("another owner's days = %v, want none", got)
	}
}

func TestTheDaysNeedAShortValidRange(t *testing.T) {
	o := manilaOwner(t)
	for _, q := range []string{"", "?from=2026-11-01", "?from=nope&to=2026-11-30", "?from=2026-11-30&to=2026-11-01", "?from=2026-01-01&to=2026-12-31"} {
		if rec := o.do(http.MethodGet, "/api/coreban/notes/days"+q, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("days %q: status = %d, want 400", q, rec.Code)
		}
	}
}
