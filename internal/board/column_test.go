package board_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

func columnsPath(b board) string { return "/api/coreban/boards/" + b.ID + "/columns" }

func columnPath(b board, c column) string { return columnsPath(b) + "/" + c.ID }

func (o owner) addColumn(b board, name, status string) column {
	o.t.Helper()
	rec := o.do(http.MethodPost, columnsPath(b), map[string]string{"name": name, "status": status})
	if rec.Code != http.StatusCreated {
		o.t.Fatalf("add column %q: status = %d, body %s", name, rec.Code, rec.Body)
	}
	return decode[column](o.t, rec)
}

// columnNames reads the board's columns in order as "name:status".
func (o owner) columnNames(b board) []string {
	o.t.Helper()
	var out []string
	for _, c := range o.get(b).Columns {
		out = append(out, c.Name+":"+c.Status)
	}
	return out
}

func refusal(t *testing.T, rec *httptest.ResponseRecorder, status int, want string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body)
	}
	if got := decode[map[string]string](t, rec)["error"]; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

func TestAnAddedColumnGoesAtTheEndWithItsStatus(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")

	review := o.addColumn(b, "  Review ", "in_progress")
	if review.Name != "Review" || review.Status != "in_progress" || review.ID == "" || review.Cards == nil {
		t.Errorf("added column = %+v, want Review, in_progress, no cards", review)
	}
	want := []string{"Todo:todo", "Doing:in_progress", "Done:done", "Review:in_progress"}
	if got := o.columnNames(b); !slices.Equal(got, want) {
		t.Errorf("columns = %v, want %v", got, want)
	}
}

func TestColumnsNeedANameAndAKnownStatus(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	refusal(t, o.do(http.MethodPost, columnsPath(b), map[string]string{"name": " ", "status": "todo"}),
		http.StatusBadRequest, "name is required")
	refusal(t, o.do(http.MethodPost, columnsPath(b), map[string]string{"name": "Blocked", "status": "blocked"}),
		http.StatusBadRequest, "status must be todo, in_progress, or done")
	refusal(t, o.do(http.MethodPatch, columnPath(b, b.Columns[0]), map[string]string{"name": ""}),
		http.StatusBadRequest, "name cannot be blank")
}

func TestRenamingAColumnKeepsItsStatusAndCards(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	o.createCard(b, b.Columns[1], "Upgrade k3s")

	rec := o.do(http.MethodPatch, columnPath(b, b.Columns[1]), map[string]string{"name": "In flight"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d, body %s", rec.Code, rec.Body)
	}
	got := decode[column](t, rec)
	if got.Name != "In flight" || got.Status != "in_progress" || len(got.Cards) != 1 {
		t.Errorf("renamed column = %+v, want In flight, in_progress, its one card", got)
	}
}

func TestReorderingAColumnMovesItToTheChosenSlot(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	review := o.addColumn(b, "Review", "in_progress")

	move := func(c column, index int) {
		t.Helper()
		rec := o.do(http.MethodPost, columnPath(b, c)+"/move", map[string]int{"index": index})
		if rec.Code != http.StatusOK {
			t.Fatalf("move %s to %d: status = %d, body %s", c.Name, index, rec.Code, rec.Body)
		}
	}
	move(review, 2)
	if got, want := o.columnNames(b), []string{"Todo:todo", "Doing:in_progress", "Review:in_progress", "Done:done"}; !slices.Equal(got, want) {
		t.Errorf("after moving Review to 2: %v, want %v", got, want)
	}
	move(b.Columns[0], 99) // clamps to the end
	if got, want := o.columnNames(b), []string{"Doing:in_progress", "Review:in_progress", "Done:done", "Todo:todo"}; !slices.Equal(got, want) {
		t.Errorf("after moving Todo past the end: %v, want %v", got, want)
	}
	move(b.Columns[0], -1) // clamps to the start
	if got := o.columnNames(b); got[0] != "Todo:todo" {
		t.Errorf("after moving Todo before the start: %v, want Todo first", got)
	}
}

func TestChangingAColumnsStatusChangesItsCardsStatus(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	o.addColumn(b, "Shipped", "done")
	review := o.addColumn(b, "Review", "in_progress")
	k := o.createCard(b, review, "Write the post")

	rec := o.do(http.MethodPatch, columnPath(b, review), map[string]string{"status": "done"})
	if rec.Code != http.StatusOK {
		t.Fatalf("change status: status = %d, body %s", rec.Code, rec.Body)
	}
	if got := decode[column](t, rec); got.Status != "done" || got.Cards[0].Status != "done" {
		t.Errorf("column = %+v, want done with its card done", got)
	}
	rec = o.do(http.MethodGet, cardsPath(b)+"/"+k.ID, nil)
	if got := decode[card](t, rec); got.Status != "done" {
		t.Errorf("card status = %q, want done", got.Status)
	}
}

func TestTheLastColumnOfAStatusCannotBeDeletedOrChangedAway(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	done := b.Columns[2]

	refusal(t, o.do(http.MethodDelete, columnPath(b, done), nil),
		http.StatusConflict, "Done is the board's only Done column; add another before removing it")
	refusal(t, o.do(http.MethodPatch, columnPath(b, done), map[string]string{"status": "todo"}),
		http.StatusConflict, "Done is the board's only Done column; add another before changing its status")
	// A rejected change leaves the column as it was, name included.
	o.do(http.MethodPatch, columnPath(b, done), map[string]string{"name": "Finished", "status": "todo"})
	if got := o.columnNames(b); got[2] != "Done:done" {
		t.Errorf("after refused change: %v, want Done:done untouched", got)
	}

	// With a second Done column, either may go.
	o.addColumn(b, "Shipped", "done")
	if rec := o.do(http.MethodPatch, columnPath(b, done), map[string]string{"status": "todo"}); rec.Code != http.StatusOK {
		t.Errorf("change status with another Done column: status = %d, body %s", rec.Code, rec.Body)
	}
}

func TestAColumnHoldingCardsCannotBeDeleted(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	review := o.addColumn(b, "Review", "in_progress")
	k := o.createCard(b, review, "Write the post")

	refusal(t, o.do(http.MethodDelete, columnPath(b, review), nil),
		http.StatusConflict, "Review still holds cards, archived ones included; move or delete them first (restore an archived card to move it)")

	o.mustMove(b, k, b.Columns[0], 0)
	if rec := o.do(http.MethodDelete, columnPath(b, review), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete emptied column: status = %d, body %s", rec.Code, rec.Body)
	}
	if got, want := o.columnNames(b), []string{"Todo:todo", "Doing:in_progress", "Done:done"}; !slices.Equal(got, want) {
		t.Errorf("columns = %v, want %v", got, want)
	}
}

func TestColumnsArePrivateAndTargetedReadsCannotChangeThem(t *testing.T) {
	alice, aliceID := newOwner(t)
	b := alice.create("Homelab")
	bob, _ := newOwner(t)
	admin := as(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"admin"}, ActAs: aliceID})

	for _, o := range []owner{bob, admin} {
		if rec := o.do(http.MethodPost, columnsPath(b), map[string]string{"name": "X", "status": "todo"}); rec.Code != http.StatusNotFound {
			t.Errorf("add: status = %d, want 404", rec.Code)
		}
		if rec := o.do(http.MethodPatch, columnPath(b, b.Columns[0]), map[string]string{"name": "X"}); rec.Code != http.StatusNotFound {
			t.Errorf("rename: status = %d, want 404", rec.Code)
		}
		if rec := o.do(http.MethodPost, columnPath(b, b.Columns[0])+"/move", map[string]int{"index": 2}); rec.Code != http.StatusNotFound {
			t.Errorf("move: status = %d, want 404", rec.Code)
		}
		if rec := o.do(http.MethodDelete, columnPath(b, b.Columns[0]), nil); rec.Code != http.StatusNotFound {
			t.Errorf("delete: status = %d, want 404", rec.Code)
		}
	}
	other := alice.create("Other")
	if rec := alice.do(http.MethodPatch, columnPath(b, other.Columns[0]), map[string]string{"name": "X"}); rec.Code != http.StatusNotFound {
		t.Errorf("column from another board: status = %d, want 404", rec.Code)
	}
	if got := alice.columnNames(b); !slices.Equal(got, []string{"Todo:todo", "Doing:in_progress", "Done:done"}) {
		t.Errorf("alice's columns changed: %v", got)
	}
}
