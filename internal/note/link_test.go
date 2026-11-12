package note_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

// A future moment for one-off reminders, read in the owner's timezone.
const later = "2030-03-05T07:00"

type reminderView struct {
	ID       string `json:"id"`
	Message  string `json:"message"`
	Schedule struct {
		Kind string `json:"kind"`
		At   string `json:"at"`
	} `json:"schedule"`
	NextFireAt *string `json:"next_fire_at"`
}

type cardView struct {
	ID         string  `json:"id"`
	BoardID    string  `json:"board_id"`
	BoardName  string  `json:"board_name"`
	ColumnName string  `json:"column_name"`
	Status     string  `json:"status"`
	Title      string  `json:"title"`
	ArchivedAt *string `json:"archived_at"`
}

type link struct {
	Kind     string        `json:"kind"`
	Deleted  bool          `json:"deleted"`
	Reminder *reminderView `json:"reminder"`
	Card     *cardView     `json:"card"`
}

type linkedNote struct {
	ID         string  `json:"id"`
	Text       string  `json:"text"`
	ArchivedAt *string `json:"archived_at"`
	Link       *link   `json:"link"`
}

type linkedPage struct {
	Notes []linkedNote `json:"notes"`
}

func (o owner) linkedFeed() []linkedNote {
	o.t.Helper()
	rec := o.do(http.MethodGet, "/api/coreban/notes", nil)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("feed: status = %d, body %s", rec.Code, rec.Body)
	}
	return decode[linkedPage](o.t, rec).Notes
}

func (o owner) newReminder(message, note string) reminderView {
	o.t.Helper()
	body := map[string]any{"message": message, "schedule": map[string]string{"kind": "once", "at": later}}
	if note != "" {
		body["note"] = note
	}
	rec := o.do(http.MethodPost, "/api/coreban/reminders", body)
	if rec.Code != http.StatusCreated {
		o.t.Fatalf("create reminder: status = %d, body %s", rec.Code, rec.Body)
	}
	return decode[reminderView](o.t, rec)
}

func TestAReminderCapturedFromTheFeedShowsThereLive(t *testing.T) {
	o, _ := newOwner(t)
	o.post("before")
	r := o.newReminder("call dentist", "new")

	feed := o.linkedFeed()
	if len(feed) != 2 {
		t.Fatalf("feed has %d notes, want the plain one and the reminder's", len(feed))
	}
	n := feed[1]
	if n.Link == nil || n.Link.Kind != "reminder" || n.Link.Deleted || n.Link.Reminder == nil {
		t.Fatalf("newest note = %+v, want a live reminder link", n)
	}
	if n.Text != "" || n.Link.Reminder.ID != r.ID || n.Link.Reminder.Message != "call dentist" || n.Link.Reminder.NextFireAt == nil {
		t.Errorf("linked reminder = %+v (text %q)", n.Link.Reminder, n.Text)
	}

	// Editing the reminder on the Reminders page changes the note.
	if rec := o.do(http.MethodPatch, "/api/coreban/reminders/"+r.ID, map[string]string{"message": "call the dentist back"}); rec.Code != http.StatusOK {
		t.Fatalf("edit reminder: status = %d, body %s", rec.Code, rec.Body)
	}
	if got := o.linkedFeed()[1].Link.Reminder.Message; got != "call the dentist back" {
		t.Errorf("note shows %q after the edit", got)
	}

	// A reminder created outside the Feed gets no note.
	o.newReminder("not from the feed", "")
	if got := len(o.linkedFeed()); got != 2 {
		t.Errorf("feed has %d notes after a plain reminder, want 2", got)
	}
}

type boardView struct {
	ID      string `json:"id"`
	Columns []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Cards []struct {
			ID string `json:"id"`
		} `json:"cards"`
	} `json:"columns"`
}

func (o owner) newBoard(name string) boardView {
	o.t.Helper()
	rec := o.do(http.MethodPost, "/api/coreban/boards", map[string]string{"name": name})
	if rec.Code != http.StatusCreated {
		o.t.Fatalf("create board: status = %d, body %s", rec.Code, rec.Body)
	}
	return decode[boardView](o.t, rec)
}

func (o owner) board(id string) boardView {
	o.t.Helper()
	rec := o.do(http.MethodGet, "/api/coreban/boards/"+id, nil)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("get board: status = %d, body %s", rec.Code, rec.Body)
	}
	return decode[boardView](o.t, rec)
}

func (o owner) newCard(b boardView, title, note string) cardView {
	o.t.Helper()
	body := map[string]any{"column_id": b.Columns[0].ID, "title": title}
	if note != "" {
		body["note"] = note
	}
	rec := o.do(http.MethodPost, "/api/coreban/boards/"+b.ID+"/cards", body)
	if rec.Code != http.StatusCreated {
		o.t.Fatalf("create card: status = %d, body %s", rec.Code, rec.Body)
	}
	return decode[cardView](o.t, rec)
}

func TestACardCapturedFromTheFeedShowsWhereItIsLive(t *testing.T) {
	o, _ := newOwner(t)
	b := o.newBoard("Homelab")
	c := o.newCard(b, "fix NAS backup", "new")

	feed := o.linkedFeed()
	if len(feed) != 1 || feed[0].Link == nil || feed[0].Link.Kind != "card" || feed[0].Link.Card == nil {
		t.Fatalf("feed = %+v, want one card note", feed)
	}
	got := *feed[0].Link.Card
	if got.ID != c.ID || got.Title != "fix NAS backup" || got.BoardName != "Homelab" ||
		got.ColumnName != "Todo" || got.Status != "todo" || got.BoardID != b.ID {
		t.Errorf("card note shows %+v", got)
	}

	// Moving the card on its board moves it in the note too.
	rec := o.do(http.MethodPost, "/api/coreban/boards/"+b.ID+"/cards/"+c.ID+"/move",
		map[string]any{"column_id": b.Columns[1].ID, "index": 0})
	if rec.Code != http.StatusOK {
		t.Fatalf("move: status = %d, body %s", rec.Code, rec.Body)
	}
	if got := o.linkedFeed()[0].Link.Card; got.ColumnName != "Doing" || got.Status != "in_progress" {
		t.Errorf("after the move the note shows %+v", got)
	}

	// A card created on the board gets no note.
	o.newCard(b, "not from the feed", "")
	if got := len(o.linkedFeed()); got != 1 {
		t.Errorf("feed has %d notes, want 1", got)
	}
}

func TestConvertingANoteLinksItInPlace(t *testing.T) {
	o, _ := newOwner(t)
	o.post("first")
	n := o.post("fix NAS backup")
	o.post("last")

	b := o.newBoard("Homelab")
	o.newCard(b, "fix NAS backup", n.ID)

	feed := o.linkedFeed()
	if len(feed) != 3 {
		t.Fatalf("feed has %d notes, want the same 3", len(feed))
	}
	got := feed[1]
	if got.ID != n.ID || got.Text != "" || got.Link == nil || got.Link.Card == nil || got.Link.Card.Title != "fix NAS backup" {
		t.Errorf("converted note = %+v, want it linked in place with no text of its own", got)
	}

	// A linked note's text can't be edited: its record is.
	if rec := o.patch(n.ID, map[string]string{"text": "x"}); rec.Code != http.StatusConflict {
		t.Errorf("edit linked text: status = %d, want 409", rec.Code)
	}
}

func TestOnlyALivePlainNoteOfYourOwnCanBeConverted(t *testing.T) {
	o, _ := newOwner(t)
	other, _ := newOwner(t)
	b := o.newBoard("Homelab")
	archived := o.post("archived")
	o.patch(archived.ID, map[string]bool{"archived": true})
	linked := o.post("will be linked")
	o.newCard(b, "linked", linked.ID)
	theirs := other.post("theirs")

	for name, note := range map[string]string{
		"archived":              archived.ID,
		"already linked":        linked.ID,
		"someone else's":        theirs.ID,
		"missing":               "00000000-0000-0000-0000-000000000001",
		"neither new nor an id": "soon",
	} {
		t.Run(name, func(t *testing.T) {
			before := len(o.board(b.ID).Columns[0].Cards)
			rec := o.do(http.MethodPost, "/api/coreban/boards/"+b.ID+"/cards",
				map[string]any{"column_id": b.Columns[0].ID, "title": "x", "note": note})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("card: status = %d, want 400 (body %s)", rec.Code, rec.Body)
			}
			if after := len(o.board(b.ID).Columns[0].Cards); after != before {
				t.Errorf("a refused card was still created (%d → %d cards)", before, after)
			}
			rec = o.do(http.MethodPost, "/api/coreban/reminders",
				map[string]any{"message": "x", "schedule": map[string]string{"kind": "once", "at": later}, "note": note})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("reminder: status = %d, want 400 (body %s)", rec.Code, rec.Body)
			}
		})
	}
	if rec := o.do(http.MethodGet, "/api/coreban/reminders", nil); len(decode[[]reminderView](t, rec)) != 0 {
		t.Errorf("a refused reminder was still created")
	}
}

func TestADeletedRecordLeavesItsNoteShowingItsLastState(t *testing.T) {
	o, _ := newOwner(t)
	b := o.newBoard("Homelab")
	card := o.newCard(b, "fix NAS backup", "new")
	alone := o.newReminder("call dentist", "new")
	other := o.newBoard("Errands")
	gone := o.newCard(other, "buy cable", "new")

	// A reminder on a card, captured from the Feed, goes with its card.
	rec := o.do(http.MethodPost, "/api/coreban/reminders", map[string]any{
		"message": "check the backup", "schedule": map[string]string{"kind": "once", "at": later},
		"card_id": card.ID, "note": "new",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("reminder on card: status = %d, body %s", rec.Code, rec.Body)
	}

	if rec := o.do(http.MethodDelete, "/api/coreban/reminders/"+alone.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete reminder: status = %d", rec.Code)
	}
	if rec := o.do(http.MethodDelete, "/api/coreban/boards/"+b.ID+"/cards/"+card.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete card: status = %d", rec.Code)
	}
	if rec := o.do(http.MethodDelete, "/api/coreban/boards/"+other.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete board: status = %d", rec.Code)
	}

	feed := o.linkedFeed()
	if len(feed) != 4 {
		t.Fatalf("feed has %d notes, want all 4 kept", len(feed))
	}
	for _, n := range feed {
		if n.Link == nil || !n.Link.Deleted {
			t.Errorf("note %+v should show a deleted record", n.Link)
		}
	}
	if c := feed[0].Link.Card; c == nil || c.Title != "fix NAS backup" || c.BoardName != "Homelab" || c.ColumnName != "Todo" {
		t.Errorf("deleted card's note shows %+v", c)
	}
	if r := feed[1].Link.Reminder; r == nil || r.Message != "call dentist" || r.Schedule.Kind != "once" {
		t.Errorf("deleted reminder's note shows %+v", r)
	}
	if c := feed[2].Link.Card; c == nil || c.Title != "buy cable" || c.BoardName != "Errands" || c.ID != gone.ID {
		t.Errorf("note of a card deleted with its board shows %+v", c)
	}
	if r := feed[3].Link.Reminder; r == nil || r.Message != "check the backup" {
		t.Errorf("note of a reminder deleted with its card shows %+v", r)
	}
}

func (o owner) cardOnBoard(b boardView, id string) bool {
	o.t.Helper()
	for _, col := range o.board(b.ID).Columns {
		for _, c := range col.Cards {
			if c.ID == id {
				return true
			}
		}
	}
	return false
}

func noteOf(feed []linkedNote, id string) linkedNote {
	for _, n := range feed {
		if n.ID == id {
			return n
		}
	}
	return linkedNote{}
}

func TestArchivingACardsNoteArchivesTheCardAndRestoreBringsItBack(t *testing.T) {
	o, _ := newOwner(t)
	b := o.newBoard("Homelab")
	c := o.newCard(b, "fix NAS backup", "new")
	n := o.linkedFeed()[0]

	rec := o.patch(n.ID, map[string]bool{"archived": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("archive: status = %d, body %s", rec.Code, rec.Body)
	}
	if got := decode[linkedNote](t, rec); got.ArchivedAt == nil || got.Link == nil || got.Link.Card == nil || got.Link.Card.ArchivedAt == nil {
		t.Errorf("archived note = %+v, want it and its card archived", got)
	}
	if o.cardOnBoard(b, c.ID) {
		t.Error("the card is still on its board")
	}

	rec = o.patch(n.ID, map[string]bool{"archived": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: status = %d, body %s", rec.Code, rec.Body)
	}
	if got := decode[linkedNote](t, rec); got.ArchivedAt != nil || got.Link.Card.ArchivedAt != nil {
		t.Errorf("restored note = %+v, want it and its card back", got)
	}
	if !o.cardOnBoard(b, c.ID) {
		t.Error("the card did not come back to its board")
	}
}

func TestRestoringANoteLeavesACardArchivedOnItsBoardAlone(t *testing.T) {
	o, _ := newOwner(t)
	b := o.newBoard("Homelab")
	c := o.newCard(b, "fix NAS backup", "new")
	n := o.linkedFeed()[0]

	// Archived on the board first: archiving the note didn't do it.
	o.do(http.MethodPatch, "/api/coreban/boards/"+b.ID+"/cards/"+c.ID, map[string]bool{"archived": true})
	o.patch(n.ID, map[string]bool{"archived": true})
	o.patch(n.ID, map[string]bool{"archived": false})
	if o.cardOnBoard(b, c.ID) {
		t.Error("restoring the note restored a card archived on its board")
	}
}

func TestArchivingAReminderNoteDeletesTheReminderForGood(t *testing.T) {
	o, _ := newOwner(t)
	r := o.newReminder("call dentist", "new")
	n := o.linkedFeed()[0]

	rec := o.patch(n.ID, map[string]bool{"archived": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("archive: status = %d, body %s", rec.Code, rec.Body)
	}
	got := decode[linkedNote](t, rec)
	if got.ArchivedAt == nil || !got.Link.Deleted || got.Link.Reminder == nil || got.Link.Reminder.Message != "call dentist" {
		t.Errorf("archived note = %+v, want it showing the deleted reminder", got)
	}
	if rec := o.do(http.MethodGet, "/api/coreban/reminders/"+r.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("reminder still there: status = %d", rec.Code)
	}

	o.patch(n.ID, map[string]bool{"archived": false})
	restored := noteOf(o.linkedFeed(), n.ID)
	if restored.ArchivedAt != nil || !restored.Link.Deleted || restored.Link.Reminder.Message != "call dentist" {
		t.Errorf("restored note = %+v, want it back, still showing the deleted reminder", restored)
	}
}

func TestARefusedRecordLeavesNoNoteBehind(t *testing.T) {
	id := uuid.New()
	s := testharness.Server(t, testharness.Identity{Owner: id, Scopes: []string{"coreban:read", "coreban:write"}})
	s.Reminders.Cap = 1
	o := owner{t: t, h: s.Routes()}
	o.newReminder("the one allowed", "")

	rec := o.do(http.MethodPost, "/api/coreban/reminders",
		map[string]any{"message": "one too many", "schedule": map[string]string{"kind": "once", "at": later}, "note": "new"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("over the cap: status = %d, want 409 (body %s)", rec.Code, rec.Body)
	}
	if got := o.linkedFeed(); len(got) != 0 {
		t.Errorf("feed = %+v, want no note for the refused reminder", got)
	}
}
