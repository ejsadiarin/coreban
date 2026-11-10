package board_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

// dueCard is a card as the due and archive tests read it.
type dueCard struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Due        map[string]string `json:"due"`
	ArchivedAt *string           `json:"archived_at"`
}

func cardPath(b board, k card) string { return cardsPath(b) + "/" + k.ID }

func (o owner) patchCard(b board, k card, body map[string]any) dueCard {
	o.t.Helper()
	rec := o.do(http.MethodPatch, cardPath(b, k), body)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("patch card %v: status = %d, body %s", body, rec.Code, rec.Body)
	}
	return decode[dueCard](o.t, rec)
}

func (o owner) archived(b board) []string {
	o.t.Helper()
	rec := o.do(http.MethodGet, cardsPath(b)+"?archived=true", nil)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("list archived: status = %d, body %s", rec.Code, rec.Body)
	}
	var titles []string
	for _, k := range decode[[]dueCard](o.t, rec) {
		titles = append(titles, k.Title)
	}
	return titles
}

func (o owner) inZone(tz string) {
	o.t.Helper()
	rec := o.do(http.MethodPut, "/api/coreban/settings", map[string]any{"ntfy_topic": nil, "timezone": tz})
	if rec.Code != http.StatusOK {
		o.t.Fatalf("set timezone: status = %d, body %s", rec.Code, rec.Body)
	}
}

func TestADueIsADateOrAMomentAndComesBackAsWhichItWas(t *testing.T) {
	o, _ := newOwner(t)
	o.inZone("Asia/Manila")
	b := o.create("Homelab")
	k := o.createCard(b, b.Columns[0], "Renew the cert")

	got := o.patchCard(b, k, map[string]any{"due": map[string]string{"date": "2026-11-14"}})
	if len(got.Due) != 1 || got.Due["date"] != "2026-11-14" {
		t.Errorf("date due = %v, want {date: 2026-11-14}", got.Due)
	}

	// No offset: read in the owner's timezone, and shown in it.
	got = o.patchCard(b, k, map[string]any{"due": map[string]string{"at": "2026-11-14T17:00"}})
	if len(got.Due) != 1 || got.Due["at"] != "2026-11-14T17:00:00+08:00" {
		t.Errorf("moment due = %v, want {at: 2026-11-14T17:00:00+08:00}", got.Due)
	}
	got = o.patchCard(b, k, map[string]any{"due": map[string]string{"at": "2026-11-14T09:00:00Z"}})
	if got.Due["at"] != "2026-11-14T17:00:00+08:00" {
		t.Errorf("instant due = %v, want the same instant in +08:00", got.Due)
	}

	// The board shows it, and an edit that leaves due out keeps it.
	o.patchCard(b, k, map[string]any{"title": "Renew the certs"})
	type boardView struct {
		Columns []struct {
			Cards []dueCard `json:"cards"`
		} `json:"columns"`
	}
	view := decode[boardView](t, o.do(http.MethodGet, "/api/coreban/boards/"+b.ID, nil))
	if d := view.Columns[0].Cards[0].Due; d["at"] != "2026-11-14T17:00:00+08:00" {
		t.Errorf("board shows due %v, want it kept", d)
	}

	got = o.patchCard(b, k, map[string]any{"due": nil})
	if got.Due != nil {
		t.Errorf("cleared due = %v, want null", got.Due)
	}
}

func TestABadDueIsRejectedWithAReason(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	k := o.createCard(b, b.Columns[0], "x")
	for _, c := range []struct {
		due  any
		want string
	}{
		{map[string]string{}, "due must have a date or an at, not both"},
		{map[string]string{"date": "2026-11-14", "at": "2026-11-14T17:00"}, "due must have a date or an at, not both"},
		{map[string]string{"date": "14/11/2026"}, "due.date must be a date such as 2026-11-14"},
		{map[string]string{"at": "soon"}, "due.at must be a date and time such as 2026-11-14T17:00"},
	} {
		refusal(t, o.do(http.MethodPatch, cardPath(b, k), map[string]any{"due": c.due}), http.StatusBadRequest, c.want)
	}
}

func TestAnArchivedCardLeavesTheBoardUntilRestoredToTheBottomOfItsColumn(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	todo := b.Columns[0]
	first := o.createCard(b, todo, "Patch the NAS")
	o.createCard(b, todo, "Rotate certs")

	got := o.patchCard(b, first, map[string]any{"archived": true})
	if got.ArchivedAt == nil {
		t.Error("archived card has no archived_at")
	}
	if titles := o.titlesByColumn(b)["Todo"]; !slices.Equal(titles, []string{"Rotate certs"}) {
		t.Errorf("Todo = %v, want the archived card hidden", titles)
	}
	if got := o.archived(b); !slices.Equal(got, []string{"Patch the NAS"}) {
		t.Errorf("archived = %v, want [Patch the NAS]", got)
	}
	// Still readable by id, and can't be moved while archived.
	if rec := o.do(http.MethodGet, cardPath(b, first), nil); rec.Code != http.StatusOK {
		t.Errorf("get archived card: status = %d", rec.Code)
	}
	refusal(t, o.move(b, first, b.Columns[1], 0), http.StatusConflict, "the card is archived; restore it first")

	if o.patchCard(b, first, map[string]any{"archived": false}).ArchivedAt != nil {
		t.Error("restored card still has archived_at")
	}
	if titles := o.titlesByColumn(b)["Todo"]; !slices.Equal(titles, []string{"Rotate certs", "Patch the NAS"}) {
		t.Errorf("Todo = %v, want the restored card at the bottom", titles)
	}
	if got := o.archived(b); len(got) != 0 {
		t.Errorf("archived = %v, want none", got)
	}
}

func TestDeletingACardIsPermanentWhetherOrNotItIsArchived(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	live := o.createCard(b, b.Columns[0], "Live")
	gone := o.createCard(b, b.Columns[0], "Archived")
	o.patchCard(b, gone, map[string]any{"archived": true})

	for _, k := range []card{live, gone} {
		if rec := o.do(http.MethodDelete, cardPath(b, k), nil); rec.Code != http.StatusNoContent {
			t.Fatalf("delete %s: status = %d, body %s", k.Title, rec.Code, rec.Body)
		}
		if rec := o.do(http.MethodGet, cardPath(b, k), nil); rec.Code != http.StatusNotFound {
			t.Errorf("get deleted %s: status = %d, want 404", k.Title, rec.Code)
		}
	}
	if rec := o.do(http.MethodDelete, cardPath(b, live), nil); rec.Code != http.StatusNotFound {
		t.Errorf("delete twice: status = %d, want 404", rec.Code)
	}
	if len(o.titlesByColumn(b)["Todo"]) != 0 || len(o.archived(b)) != 0 {
		t.Error("deleted cards are still listed")
	}
}

func TestAColumnHoldingOnlyArchivedCardsStillCannotBeDeleted(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	review := o.addColumn(b, "Review", "in_progress")
	k := o.createCard(b, review, "Old")
	o.patchCard(b, k, map[string]any{"archived": true})

	refusal(t, o.do(http.MethodDelete, columnPath(b, review), nil), http.StatusConflict,
		"Review still holds cards, archived ones included; move or delete them first (restore an archived card to move it)")
}

func TestOnlyTheOwnerArchivesListsOrDeletesCards(t *testing.T) {
	alice, aliceID := newOwner(t)
	b := alice.create("Homelab")
	k := alice.createCard(b, b.Columns[0], "Mine")
	alice.patchCard(b, alice.createCard(b, b.Columns[0], "Old"), map[string]any{"archived": true})

	bob, _ := newOwner(t)
	if rec := bob.do(http.MethodPatch, cardPath(b, k), map[string]any{"archived": true}); rec.Code != http.StatusNotFound {
		t.Errorf("bob archives: status = %d, want 404", rec.Code)
	}
	if rec := bob.do(http.MethodDelete, cardPath(b, k), nil); rec.Code != http.StatusNotFound {
		t.Errorf("bob deletes: status = %d, want 404", rec.Code)
	}
	if rec := bob.do(http.MethodGet, cardsPath(b)+"?archived=true", nil); rec.Code != http.StatusNotFound {
		t.Errorf("bob lists archived: status = %d, want 404", rec.Code)
	}

	admin := as(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"admin"}, ActAs: aliceID})
	if got := admin.archived(b); !slices.Equal(got, []string{"Old"}) {
		t.Errorf("targeted read of archived = %v, want [Old]", got)
	}
	if rec := admin.do(http.MethodDelete, cardPath(b, k), nil); rec.Code != http.StatusNotFound {
		t.Errorf("targeted read deletes: status = %d, want 404", rec.Code)
	}
}

func TestArchivedCardsTakeNoSlotWhenCardsMoveAround(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	todo, doing := b.Columns[0], b.Columns[1]
	o.createCard(b, todo, "A")
	hidden := o.createCard(b, todo, "Hidden")
	o.createCard(b, todo, "B")
	o.patchCard(b, hidden, map[string]any{"archived": true})
	mover := o.createCard(b, doing, "Mover")

	// Index 1 is between A and B: the archived card between them is not a slot.
	o.mustMove(b, mover, todo, 1)
	if got := o.titlesByColumn(b)["Todo"]; !slices.Equal(got, []string{"A", "Mover", "B"}) {
		t.Errorf("Todo = %v, want [A Mover B]", got)
	}
}
