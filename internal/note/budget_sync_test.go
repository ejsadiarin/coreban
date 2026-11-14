package note_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

// syncedNote is a note as the sync-aware web reads it: where it sits
// (posted_at) and who made it (origin).
type syncedNote struct {
	ID       string      `json:"id"`
	Text     string      `json:"text"`
	PostedAt time.Time   `json:"posted_at"`
	Origin   string      `json:"origin"`
	Link     *budgetLink `json:"link"`
}

type syncedPage struct {
	Notes []syncedNote `json:"notes"`
	Older *string      `json:"older"`
}

type syncRecord struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Date     string `json:"date"`
	PostedAt string `json:"posted_at"`
}

type syncResult struct {
	Created int `json:"created"`
	Moved   int `json:"moved"`
}

const manila = "Asia/Manila"

func (o owner) sync(records ...syncRecord) syncResult {
	o.t.Helper()
	rec := o.do(http.MethodPost, "/api/coreban/notes/sync", map[string]any{"timezone": manila, "records": records})
	if rec.Code != http.StatusOK {
		o.t.Fatalf("sync: status = %d, body %s", rec.Code, rec.Body)
	}
	return decode[syncResult](o.t, rec)
}

func (o owner) syncedFeed(query string) syncedPage {
	o.t.Helper()
	rec := o.do(http.MethodGet, "/api/coreban/notes"+query, nil)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("feed %q: status = %d, body %s", query, rec.Code, rec.Body)
	}
	return decode[syncedPage](o.t, rec)
}

// at is a local Manila time as the web sends it.
func at(s string) string { return s + "+08:00" }

func record(kind, date, clock string) syncRecord {
	return syncRecord{Kind: kind, ID: uuid.NewString(), Date: date, PostedAt: at(date + "T" + clock)}
}

func linked(ns []syncedNote) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		if n.Link != nil {
			out = append(out, n.Link.RecordID)
		} else {
			out = append(out, "text:"+n.Text)
		}
	}
	return out
}

func TestSyncedRecordsSitOnTheirOwnDatesBeforeANoteFromToday(t *testing.T) {
	o, _ := newOwner(t)
	o.post("today")
	may5 := record("expense", "2026-05-05", "10:10:00")
	jun2 := record("income", "2026-06-02", "12:00:00")

	if got := o.sync(jun2, may5); got != (syncResult{Created: 2}) {
		t.Errorf("first sync = %+v, want 2 created", got)
	}
	feed := o.syncedFeed("").Notes
	want := []string{may5.ID, jun2.ID, "text:today"}
	if got := linked(feed); !equal(got, want) {
		t.Fatalf("feed = %v, want %v (oldest first)", got, want)
	}
	if feed[0].Origin != "budget" || feed[2].Origin != "feed" {
		t.Errorf("origins = %q, %q; want budget for the synced note, feed for the posted one", feed[0].Origin, feed[2].Origin)
	}
	if want, _ := time.Parse(time.RFC3339, may5.PostedAt); !feed[0].PostedAt.Equal(want) {
		t.Errorf("synced note posted_at = %v, want %v", feed[0].PostedAt, want)
	}

	if got := o.sync(jun2, may5); got != (syncResult{}) {
		t.Errorf("second sync = %+v, want nothing created or moved", got)
	}
	if n := len(o.syncedFeed("").Notes); n != 3 {
		t.Errorf("feed has %d notes after a repeat sync, want 3", n)
	}
}

func TestARedatedRecordMovesItsNoteAndAnUnchangedOneKeepsItsTime(t *testing.T) {
	o, _ := newOwner(t)
	early := record("expense", "2026-05-05", "08:00:00")
	late := record("expense", "2026-05-06", "09:00:00")
	o.sync(early, late)

	// Same date, a different time: the note keeps the time it has.
	sameDay := early
	sameDay.PostedAt = at("2026-05-05T12:00:00")
	// Re-dated past the other record.
	moved := late
	moved.Date, moved.PostedAt = "2026-04-01", at("2026-04-01T12:00:00")

	if got := o.sync(sameDay, moved); got != (syncResult{Moved: 1}) {
		t.Errorf("sync = %+v, want 1 moved", got)
	}
	feed := o.syncedFeed("").Notes
	if got, want := linked(feed), []string{late.ID, early.ID}; !equal(got, want) {
		t.Fatalf("feed = %v, want the re-dated record first", got)
	}
	if want, _ := time.Parse(time.RFC3339, early.PostedAt); !feed[1].PostedAt.Equal(want) {
		t.Errorf("unchanged record's note moved to %v, want it kept at %v", feed[1].PostedAt, want)
	}
}

func TestSyncTakesOnlyExpensesAndIncomesWithDatesAndATimezone(t *testing.T) {
	o, _ := newOwner(t)
	bad := []map[string]any{
		{"timezone": manila, "records": []syncRecord{{Kind: "card", ID: uuid.NewString(), Date: "2026-05-05", PostedAt: at("2026-05-05T12:00:00")}}},
		{"timezone": manila, "records": []syncRecord{{Kind: "expense", ID: "nope", Date: "2026-05-05", PostedAt: at("2026-05-05T12:00:00")}}},
		{"timezone": manila, "records": []syncRecord{{Kind: "expense", ID: uuid.NewString(), Date: "May 5", PostedAt: at("2026-05-05T12:00:00")}}},
		{"timezone": "Mars/Olympus", "records": []syncRecord{record("expense", "2026-05-05", "12:00:00")}},
	}
	for i, body := range bad {
		if rec := o.do(http.MethodPost, "/api/coreban/notes/sync", body); rec.Code != http.StatusBadRequest {
			t.Errorf("bad sync %d: status = %d, want 400 (body %s)", i, rec.Code, rec.Body)
		}
	}
}

func TestDeletingByRecordRemovesOnlyASyncedNote(t *testing.T) {
	o, _ := newOwner(t)
	synced := record("expense", "2026-05-05", "12:00:00")
	o.sync(synced)
	captured := uuid.NewString()
	o.newBudgetNote("income", captured)

	if rec := o.do(http.MethodDelete, "/api/coreban/notes/records/expense/"+synced.ID, nil); rec.Code != http.StatusNoContent {
		t.Errorf("delete synced: status = %d, want 204 (body %s)", rec.Code, rec.Body)
	}
	if rec := o.do(http.MethodDelete, "/api/coreban/notes/records/income/"+captured, nil); rec.Code != http.StatusConflict {
		t.Errorf("delete captured: status = %d, want 409 (body %s)", rec.Code, rec.Body)
	}
	if rec := o.do(http.MethodDelete, "/api/coreban/notes/records/expense/"+uuid.NewString(), nil); rec.Code != http.StatusNotFound {
		t.Errorf("delete unknown: status = %d, want 404", rec.Code)
	}
	if got, want := linked(o.syncedFeed("").Notes), []string{captured}; !equal(got, want) {
		t.Errorf("feed = %v, want only the captured note", got)
	}

	other, _ := newOwner(t)
	o.sync(synced)
	if rec := other.do(http.MethodDelete, "/api/coreban/notes/records/expense/"+synced.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("another owner deleting: status = %d, want 404", rec.Code)
	}
}

func TestTheFeedFiltersToBudgetOrToNotesAndPagesWithoutGaps(t *testing.T) {
	o, _ := newOwner(t)
	var budget []string
	for _, day := range []string{"01", "02", "02", "03", "04"} {
		r := record("expense", "2026-05-"+day, "12:00:00")
		o.sync(r)
		budget = append(budget, r.ID)
	}
	o.post("a")
	o.post("b")

	var got []string
	query := "?only=budget&limit=2"
	for range 10 {
		p := o.syncedFeed(query)
		got = append(linked(p.Notes), got...)
		if p.Older == nil {
			break
		}
		query = "?only=budget&limit=2&before=" + *p.Older
	}
	if !equal(got, budget) {
		t.Errorf("budget pages = %v, want %v", got, budget)
	}
	if notes := linked(o.syncedFeed("?only=notes").Notes); !equal(notes, []string{"text:a", "text:b"}) {
		t.Errorf("notes filter = %v, want the two posted notes", notes)
	}
	if rec := o.do(http.MethodGet, "/api/coreban/notes?only=cards", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown filter: status = %d, want 400", rec.Code)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
