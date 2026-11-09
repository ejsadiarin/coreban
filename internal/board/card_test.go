package board_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

type card struct {
	ID          string `json:"id"`
	BoardID     string `json:"board_id"`
	ColumnID    string `json:"column_id"`
	Status      string `json:"status"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func cardsPath(b board) string { return "/api/coreban/boards/" + b.ID + "/cards" }

func (o owner) createCard(b board, col column, title string) card {
	o.t.Helper()
	rec := o.do(http.MethodPost, cardsPath(b), map[string]string{"column_id": col.ID, "title": title})
	if rec.Code != http.StatusCreated {
		o.t.Fatalf("create card %q: status = %d, body %s", title, rec.Code, rec.Body)
	}
	return decode[card](o.t, rec)
}

func (o owner) get(b board) board {
	o.t.Helper()
	rec := o.do(http.MethodGet, "/api/coreban/boards/"+b.ID, nil)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("get board: status = %d, body %s", rec.Code, rec.Body)
	}
	return decode[board](o.t, rec)
}

// titlesByColumn reads the board and returns each column's card titles in
// order, keyed by column name.
func (o owner) titlesByColumn(b board) map[string][]string {
	o.t.Helper()
	out := map[string][]string{}
	for _, c := range o.get(b).Columns {
		titles := []string{}
		for _, k := range c.Cards {
			titles = append(titles, k.Title)
		}
		out[c.Name] = titles
	}
	return out
}

func TestCreatedCardsAppearInTheirColumnInCreationOrderWithTheColumnsStatus(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	todo, doing := b.Columns[0], b.Columns[1]

	first := o.createCard(b, todo, "  Patch the NAS ")
	o.createCard(b, todo, "Rotate certs")
	o.createCard(b, doing, "Upgrade k3s")

	if first.Title != "Patch the NAS" || first.Status != "todo" || first.ColumnID != todo.ID ||
		first.BoardID != b.ID || first.ID == "" || first.Description != "" {
		t.Errorf("created card = %+v, want trimmed title, status todo, in Todo on this board", first)
	}

	got := o.titlesByColumn(b)
	if !slices.Equal(got["Todo"], []string{"Patch the NAS", "Rotate certs"}) ||
		!slices.Equal(got["Doing"], []string{"Upgrade k3s"}) || len(got["Done"]) != 0 {
		t.Errorf("board cards = %v, want Todo [Patch the NAS Rotate certs], Doing [Upgrade k3s], Done []", got)
	}
	for _, c := range o.get(b).Columns {
		for _, k := range c.Cards {
			if k.Status != c.Status || k.ColumnID != c.ID {
				t.Errorf("card %q in %s reports status %q column %s", k.Title, c.Name, k.Status, k.ColumnID)
			}
		}
	}
}

func TestOwnerEditsACardsTitleAndMarkdownDescription(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Daily")
	c := o.createCard(b, b.Columns[0], "Shell drill")
	path := cardsPath(b) + "/" + c.ID

	desc := "Practise:\n\n```sh\nps aux | awk '{print $2}'\n```\n"
	rec := o.do(http.MethodPatch, path, map[string]string{"description": desc})
	if got := decode[card](t, rec); rec.Code != http.StatusOK || got.Description != desc || got.Title != "Shell drill" {
		t.Fatalf("set description: status = %d, card %+v; want description set, title kept", rec.Code, got)
	}
	rec = o.do(http.MethodPatch, path, map[string]string{"title": " Shell drills "})
	if got := decode[card](t, rec); rec.Code != http.StatusOK || got.Title != "Shell drills" || got.Description != desc {
		t.Fatalf("retitle: status = %d, card %+v; want title trimmed, description kept", rec.Code, got)
	}

	rec = o.do(http.MethodGet, path, nil)
	got := decode[card](t, rec)
	if rec.Code != http.StatusOK || got.Title != "Shell drills" || got.Description != desc || got.Status != "todo" {
		t.Errorf("get: status = %d, card %+v; want edited card with status todo", rec.Code, got)
	}

	// The description may be cleared; the title may not.
	rec = o.do(http.MethodPatch, path, map[string]string{"description": ""})
	if got := decode[card](t, rec); rec.Code != http.StatusOK || got.Description != "" {
		t.Errorf("clear description: status = %d, card %+v", rec.Code, got)
	}
	for _, body := range []any{map[string]string{"title": "  "}, "not an object"} {
		if rec := o.do(http.MethodPatch, path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("patch %v: status = %d, want 400", body, rec.Code)
		}
	}
	if got := o.titlesByColumn(b)["Todo"]; !slices.Equal(got, []string{"Shell drills"}) {
		t.Errorf("Todo = %v, want [Shell drills]", got)
	}
}

func TestCardsNeedATitleAndAColumnOnTheirBoard(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Job hunt")
	other := o.create("Other")
	for name, body := range map[string]any{
		"blank title":          map[string]string{"column_id": b.Columns[0].ID, "title": "  "},
		"no column":            map[string]string{"title": "x"},
		"other board's column": map[string]string{"column_id": other.Columns[0].ID, "title": "x"},
		"not an object":        "x",
	} {
		if rec := o.do(http.MethodPost, cardsPath(b), body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, body %s; want 400", name, rec.Code, rec.Body)
		}
	}
	if rec := o.do(http.MethodGet, cardsPath(b)+"/not-a-uuid", nil); rec.Code != http.StatusNotFound {
		t.Errorf("malformed card id: status = %d, want 404", rec.Code)
	}
	c := o.createCard(other, other.Columns[0], "Elsewhere")
	if rec := o.do(http.MethodGet, cardsPath(b)+"/"+c.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("card through the wrong board: status = %d, want 404", rec.Code)
	}
}

func (o owner) move(b board, c card, col column, index int) *httptest.ResponseRecorder {
	o.t.Helper()
	return o.do(http.MethodPost, cardsPath(b)+"/"+c.ID+"/move", map[string]any{"column_id": col.ID, "index": index})
}

func (o owner) mustMove(b board, c card, col column, index int) card {
	o.t.Helper()
	rec := o.move(b, c, col, index)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("move %q to %s[%d]: status = %d, body %s", c.Title, col.Name, index, rec.Code, rec.Body)
	}
	return decode[card](o.t, rec)
}

func TestMovingACardToAnotherColumnChangesItsStatus(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	todo, doing, done := b.Columns[0], b.Columns[1], b.Columns[2]
	c := o.createCard(b, todo, "Upgrade k3s")
	o.createCard(b, doing, "Rotate certs")

	moved := o.mustMove(b, c, doing, 0)
	if moved.Status != "in_progress" || moved.ColumnID != doing.ID {
		t.Errorf("after move to Doing: %+v, want status in_progress in Doing", moved)
	}
	if got := o.titlesByColumn(b); len(got["Todo"]) != 0 || !slices.Equal(got["Doing"], []string{"Upgrade k3s", "Rotate certs"}) {
		t.Errorf("board = %v, want Todo [], Doing [Upgrade k3s Rotate certs]", got)
	}

	o.mustMove(b, c, done, 0)
	rec := o.do(http.MethodGet, cardsPath(b)+"/"+c.ID, nil)
	if got := decode[card](t, rec); got.Status != "done" {
		t.Errorf("get after move to Done: status %q, want done", got.Status)
	}
}

func TestReorderingWithinAColumnInsertsAtTheChosenSlot(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Daily")
	todo := b.Columns[0]
	a := o.createCard(b, todo, "A")
	o.createCard(b, todo, "B")
	c := o.createCard(b, todo, "C")
	o.createCard(b, todo, "D")

	// Index is the slot among the column's other cards.
	steps := []struct {
		card  card
		index int
		want  []string
	}{
		{c, 0, []string{"C", "A", "B", "D"}},
		{a, 3, []string{"C", "B", "D", "A"}},
		{a, 1, []string{"C", "A", "B", "D"}},
		{c, 99, []string{"A", "B", "D", "C"}}, // past the end clamps to last
		{c, -5, []string{"C", "A", "B", "D"}}, // before the start clamps to first
	}
	for _, s := range steps {
		o.mustMove(b, s.card, todo, s.index)
		if got := o.titlesByColumn(b)["Todo"]; !slices.Equal(got, s.want) {
			t.Fatalf("move %s to %d: Todo = %v, want %v", s.card.Title, s.index, got, s.want)
		}
	}
}

func TestRepeatedInsertsBetweenTheSameNeighboursKeepTheirOrder(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Priorities")
	todo := b.Columns[0]
	o.createCard(b, todo, "first")
	o.createCard(b, todo, "last")

	// Each new card goes into slot 1, halving the gap after "first" until a
	// midpoint no longer fits; order must hold throughout.
	want := []string{"last"}
	for i := 0; i < 70; i++ {
		title := fmt.Sprintf("n%02d", i)
		o.mustMove(b, o.createCard(b, todo, title), todo, 1)
		want = append([]string{title}, want...)
	}
	want = append([]string{"first"}, want...)
	if got := o.titlesByColumn(b)["Todo"]; !slices.Equal(got, want) {
		t.Errorf("Todo =\n%v\nwant\n%v", got, want)
	}
}

func TestMovesNeedAColumnOnTheSameBoard(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	other := o.create("Other")
	c := o.createCard(b, b.Columns[0], "Stay put")
	if rec := o.move(b, c, other.Columns[1], 0); rec.Code != http.StatusBadRequest {
		t.Errorf("move to another board's column: status = %d, want 400", rec.Code)
	}
	if rec := o.do(http.MethodPost, cardsPath(b)+"/"+c.ID+"/move", map[string]any{"index": 0}); rec.Code != http.StatusBadRequest {
		t.Errorf("move without column: status = %d, want 400", rec.Code)
	}
	if got := o.titlesByColumn(b)["Todo"]; !slices.Equal(got, []string{"Stay put"}) {
		t.Errorf("Todo = %v, want [Stay put]", got)
	}
}

func TestCardsArePrivateAndTargetedReadsCannotWrite(t *testing.T) {
	target, targetID := newOwner(t)
	b := target.create("Private")
	c := target.createCard(b, b.Columns[0], "Secret")
	path := cardsPath(b) + "/" + c.ID

	intruder, _ := newOwner(t)
	admin := as(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"admin"}, ActAs: targetID})

	// Reads: the admin's targeted read sees the card; another owner doesn't.
	if rec := admin.do(http.MethodGet, path, nil); rec.Code != http.StatusOK || decode[card](t, rec).Title != "Secret" {
		t.Errorf("admin targeted get card: status = %d, body %s; want 200 Secret", rec.Code, rec.Body)
	}
	if got := admin.titlesByColumn(b)["Todo"]; !slices.Equal(got, []string{"Secret"}) {
		t.Errorf("admin targeted board read: Todo = %v, want [Secret]", got)
	}
	if rec := intruder.do(http.MethodGet, path, nil); rec.Code != http.StatusNotFound {
		t.Errorf("intruder get card: status = %d, want 404", rec.Code)
	}

	// Writes: neither can create, edit, or move; both bind to themselves.
	for name, o := range map[string]owner{"intruder": intruder, "admin": admin} {
		writes := []struct {
			method, path string
			body         any
		}{
			{http.MethodPost, cardsPath(b), map[string]string{"column_id": b.Columns[0].ID, "title": "pwned"}},
			{http.MethodPatch, path, map[string]string{"title": "pwned"}},
			{http.MethodPost, path + "/move", map[string]any{"column_id": b.Columns[2].ID, "index": 0}},
		}
		for _, w := range writes {
			if rec := o.do(w.method, w.path, w.body); rec.Code != http.StatusNotFound {
				t.Errorf("%s %s %s: status = %d, want 404", name, w.method, w.path, rec.Code)
			}
		}
	}
	got := target.titlesByColumn(b)
	if !slices.Equal(got["Todo"], []string{"Secret"}) || len(got["Done"]) != 0 {
		t.Errorf("target's board after attempts = %v, want only Secret in Todo", got)
	}
}

func TestDeletingABoardTakesItsCardsWithIt(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Short-lived")
	for _, col := range b.Columns {
		o.createCard(b, col, "in "+col.Name)
	}
	c := o.createCard(b, b.Columns[0], "last")
	if rec := o.do(http.MethodDelete, "/api/coreban/boards/"+b.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete board with cards: status = %d, body %s; want 204", rec.Code, rec.Body)
	}
	if rec := o.do(http.MethodGet, cardsPath(b)+"/"+c.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("card on deleted board: status = %d, want 404", rec.Code)
	}
}
