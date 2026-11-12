package note_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// budgetLink is a linked expense or income as coreban shows it: only the
// record's id while it lives in corefinance, and the snapshot the web
// saved once it found the record gone.
type budgetLink struct {
	Kind     string          `json:"kind"`
	Deleted  bool            `json:"deleted"`
	RecordID string          `json:"record_id"`
	Expense  json.RawMessage `json:"expense"`
	Income   json.RawMessage `json:"income"`
}

type budgetNote struct {
	ID         string      `json:"id"`
	Text       string      `json:"text"`
	ArchivedAt *string     `json:"archived_at"`
	Link       *budgetLink `json:"link"`
}

func (o owner) postLink(kind, recordID string) *http.Response {
	o.t.Helper()
	return o.do(http.MethodPost, "/api/coreban/notes",
		map[string]any{"link": map[string]string{"kind": kind, "id": recordID}}).Result()
}

func (o owner) newBudgetNote(kind, recordID string) budgetNote {
	o.t.Helper()
	rec := o.do(http.MethodPost, "/api/coreban/notes",
		map[string]any{"link": map[string]string{"kind": kind, "id": recordID}})
	if rec.Code != http.StatusCreated {
		o.t.Fatalf("link %s: status = %d, body %s", kind, rec.Code, rec.Body)
	}
	return decode[budgetNote](o.t, rec)
}

func (o owner) budgetFeed() []budgetNote {
	o.t.Helper()
	rec := o.do(http.MethodGet, "/api/coreban/notes", nil)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("feed: status = %d, body %s", rec.Code, rec.Body)
	}
	return decode[struct {
		Notes []budgetNote `json:"notes"`
	}](o.t, rec).Notes
}

func TestAnExpenseOrIncomeCapturedFromTheFeedGetsANoteByItsID(t *testing.T) {
	o, _ := newOwner(t)
	o.post("before")
	expense, income := uuid.NewString(), uuid.NewString()

	e := o.newBudgetNote("expense", expense)
	if e.Text != "" || e.Link == nil || e.Link.Kind != "expense" || e.Link.RecordID != expense || e.Link.Deleted {
		t.Errorf("expense note = %+v, want a live link to %s", e, expense)
	}
	if e.Link != nil && e.Link.Expense != nil {
		t.Errorf("a live expense's note carries content %s; the web reads it from corefinance", e.Link.Expense)
	}
	o.newBudgetNote("income", income)

	feed := o.budgetFeed()
	if len(feed) != 3 {
		t.Fatalf("feed has %d notes, want the plain one and both budget notes", len(feed))
	}
	if got := feed[1].Link; got == nil || got.Kind != "expense" || got.RecordID != expense {
		t.Errorf("second note links %+v, want the expense", got)
	}
	if got := feed[2].Link; got == nil || got.Kind != "income" || got.RecordID != income || got.Deleted {
		t.Errorf("newest note links %+v, want the live income", got)
	}
}

func TestABudgetLinkIsRefusedUnlessItNamesOneNewExpenseOrIncome(t *testing.T) {
	o, _ := newOwner(t)
	taken := uuid.NewString()
	o.newBudgetNote("expense", taken)

	for name, body := range map[string]any{
		"a reminder":      map[string]any{"link": map[string]string{"kind": "reminder", "id": uuid.NewString()}},
		"a card":          map[string]any{"link": map[string]string{"kind": "card", "id": uuid.NewString()}},
		"an unknown kind": map[string]any{"link": map[string]string{"kind": "invoice", "id": uuid.NewString()}},
		"a malformed id":  map[string]any{"link": map[string]string{"kind": "expense", "id": "lunch"}},
		"text and a link": map[string]any{"text": "lunch", "link": map[string]string{"kind": "expense", "id": uuid.NewString()}},
		"neither":         map[string]any{},
	} {
		t.Run(name, func(t *testing.T) {
			if rec := o.do(http.MethodPost, "/api/coreban/notes", body); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body %s)", rec.Code, rec.Body)
			}
		})
	}

	// A record has at most one note.
	if res := o.postLink("expense", taken); res.StatusCode != http.StatusConflict {
		t.Errorf("second note for one expense: status = %d, want 409", res.StatusCode)
	}
	if got := len(o.budgetFeed()); got != 1 {
		t.Errorf("feed has %d notes after the refusals, want 1", got)
	}
}

func TestTheWebSavesAMissingRecordsLastContentAndTheFirstSnapshotWins(t *testing.T) {
	o, _ := newOwner(t)
	n := o.newBudgetNote("expense", uuid.NewString())

	first := map[string]any{"amount": 250, "description": "Lunch"}
	rec := o.patch(n.ID, map[string]any{"snapshot": first})
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot: status = %d, body %s", rec.Code, rec.Body)
	}
	got := decode[budgetNote](t, rec)
	if got.Link == nil || !got.Link.Deleted || got.Link.RecordID == "" {
		t.Fatalf("note after its snapshot = %+v, want it deleted, still naming its record", got.Link)
	}
	var shown map[string]any
	if err := json.Unmarshal(got.Link.Expense, &shown); err != nil || shown["description"] != "Lunch" {
		t.Errorf("deleted expense shows %s, want the snapshot", got.Link.Expense)
	}

	// Another device that also found it missing saves again: a no-op.
	rec = o.patch(n.ID, map[string]any{"snapshot": map[string]any{"amount": 999, "description": "Dinner"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("second snapshot: status = %d, body %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(o.budgetFeed()[0].Link.Expense, &shown); err != nil || shown["description"] != "Lunch" {
		t.Errorf("after a second snapshot the note shows %v, want the first", shown)
	}
}

func TestOnlyABudgetNoteTakesASnapshotFromTheWeb(t *testing.T) {
	o, _ := newOwner(t)
	plain := o.post("plain")
	o.newReminder("call dentist", "new")
	reminderNote := o.linkedFeed()[1]
	budget := o.newBudgetNote("income", uuid.NewString())

	snap := map[string]any{"snapshot": map[string]any{"amount": 1}}
	for name, id := range map[string]string{"plain": plain.ID, "reminder": reminderNote.ID} {
		if rec := o.patch(id, snap); rec.Code != http.StatusConflict {
			t.Errorf("%s note: status = %d, want 409 (body %s)", name, rec.Code, rec.Body)
		}
	}
	for name, body := range map[string]any{
		"not an object": map[string]any{"snapshot": "gone"},
		"null":          map[string]any{"snapshot": nil},
		"an array":      map[string]any{"snapshot": []int{1}},
	} {
		if rec := o.patch(budget.ID, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %s)", name, rec.Code, rec.Body)
		}
	}
	if got := o.budgetFeed()[2].Link; got.Deleted || got.Income != nil {
		t.Errorf("refused snapshots changed the note: %+v", got)
	}
}

func TestArchivingABudgetNoteOnlyArchivesTheNote(t *testing.T) {
	o, _ := newOwner(t)
	n := o.newBudgetNote("expense", uuid.NewString())

	// The web has deleted the expense in corefinance already; it archives
	// the note with the content it last showed, in one request.
	rec := o.patch(n.ID, map[string]any{"archived": true, "snapshot": map[string]any{"description": "Lunch"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("archive: status = %d, body %s", rec.Code, rec.Body)
	}
	got := decode[budgetNote](t, rec)
	if got.ArchivedAt == nil || !got.Link.Deleted || got.Link.Expense == nil {
		t.Errorf("archived note = %+v, want it archived, showing its deleted expense", got.Link)
	}

	// Restoring brings the note back; the expense stays deleted.
	rec = o.patch(n.ID, map[string]bool{"archived": false})
	if got := decode[budgetNote](t, rec); rec.Code != http.StatusOK || got.ArchivedAt != nil || !got.Link.Deleted {
		t.Errorf("restored note = %+v (status %d)", got, rec.Code)
	}
}

func TestABudgetNoteIsYoursAlone(t *testing.T) {
	o, _ := newOwner(t)
	other, _ := newOwner(t)
	shared := uuid.NewString()
	n := o.newBudgetNote("expense", shared)

	if rec := other.patch(n.ID, map[string]any{"snapshot": map[string]any{"amount": 1}}); rec.Code != http.StatusNotFound {
		t.Errorf("someone else's snapshot: status = %d, want 404", rec.Code)
	}
	// Someone who somehow knows the id can't block your note, and their
	// note stays in their own feed.
	if res := other.postLink("expense", shared); res.StatusCode != http.StatusCreated {
		t.Errorf("another owner linking the same id: status = %d, want 201", res.StatusCode)
	}
	if got := other.budgetFeed(); len(got) != 1 || got[0].ID == n.ID {
		t.Errorf("their feed = %+v, want only their own note", got)
	}
	if got := o.budgetFeed(); len(got) != 1 || got[0].Link.Deleted {
		t.Errorf("your feed = %+v, want your one live note", got)
	}
}
