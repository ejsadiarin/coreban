package board_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

type label struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func labelsPath(b board) string { return "/api/coreban/boards/" + b.ID + "/labels" }

func (o owner) addLabel(b board, name string) label {
	o.t.Helper()
	rec := o.do(http.MethodPost, labelsPath(b), map[string]string{"name": name})
	if rec.Code != http.StatusCreated {
		o.t.Fatalf("add label %q: status = %d, body %s", name, rec.Code, rec.Body)
	}
	return decode[label](o.t, rec)
}

func (o owner) labelNames(b board) []string {
	o.t.Helper()
	rec := o.do(http.MethodGet, labelsPath(b), nil)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("list labels: status = %d, body %s", rec.Code, rec.Body)
	}
	var out []string
	for _, l := range decode[[]label](o.t, rec) {
		out = append(out, l.Name)
	}
	return out
}

func cardLabelPath(b board, k card, l label) string { return cardPath(b, k) + "/labels/" + l.ID }

// cardLabels reads a card's label names, in the order the API gives them.
func (o owner) cardLabels(b board, k card) []string {
	o.t.Helper()
	type labelled struct {
		Labels []label `json:"labels"`
	}
	got := decode[labelled](o.t, o.do(http.MethodGet, cardPath(b, k), nil))
	names := []string{}
	for _, l := range got.Labels {
		names = append(names, l.Name)
	}
	return names
}

func TestLabelsAreCreatedRenamedAndDeletedPerBoard(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	k8s := o.addLabel(b, "  k8s ")
	o.addLabel(b, "Urgent")
	if k8s.Name != "k8s" || k8s.ID == "" {
		t.Errorf("label = %+v, want trimmed k8s with an id", k8s)
	}
	if got := o.labelNames(b); !slices.Equal(got, []string{"k8s", "Urgent"}) {
		t.Errorf("labels = %v, want [k8s Urgent] by name", got)
	}

	rec := o.do(http.MethodPatch, labelsPath(b)+"/"+k8s.ID, map[string]string{"name": "Kubernetes"})
	if rec.Code != http.StatusOK || decode[label](t, rec).Name != "Kubernetes" {
		t.Fatalf("rename: status = %d, body %s", rec.Code, rec.Body)
	}
	if rec := o.do(http.MethodDelete, labelsPath(b)+"/"+k8s.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, body %s", rec.Code, rec.Body)
	}
	if got := o.labelNames(b); !slices.Equal(got, []string{"Urgent"}) {
		t.Errorf("labels after delete = %v, want [Urgent]", got)
	}

	// Another board has its own labels.
	other := o.create("Job hunt")
	if got := o.labelNames(other); len(got) != 0 {
		t.Errorf("a new board has labels %v", got)
	}
	o.addLabel(other, "Urgent")
}

func TestLabelNamesAreRequiredAndUniqueWithinABoard(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	urgent := o.addLabel(b, "Urgent")
	other := o.addLabel(b, "Later")

	refusal(t, o.do(http.MethodPost, labelsPath(b), map[string]string{"name": " "}), http.StatusBadRequest, "name is required")
	refusal(t, o.do(http.MethodPost, labelsPath(b), map[string]string{"name": "urgent"}),
		http.StatusConflict, `this board already has a label named "urgent"`)
	refusal(t, o.do(http.MethodPatch, labelsPath(b)+"/"+other.ID, map[string]string{"name": "URGENT"}),
		http.StatusConflict, `this board already has a label named "URGENT"`)
	// Renaming a label to itself, in another case, is fine.
	if rec := o.do(http.MethodPatch, labelsPath(b)+"/"+urgent.ID, map[string]string{"name": "URGENT"}); rec.Code != http.StatusOK {
		t.Errorf("recase own name: status = %d, body %s", rec.Code, rec.Body)
	}
}

func TestLabelsAreAppliedToAndRemovedFromCards(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	k := o.createCard(b, b.Columns[0], "Upgrade k3s")
	k8s, urgent := o.addLabel(b, "k8s"), o.addLabel(b, "Urgent")

	for _, l := range []label{urgent, k8s, k8s} { // applying twice is harmless
		if rec := o.do(http.MethodPut, cardLabelPath(b, k, l), nil); rec.Code != http.StatusNoContent {
			t.Fatalf("apply %s: status = %d, body %s", l.Name, rec.Code, rec.Body)
		}
	}
	if got := o.cardLabels(b, k); !slices.Equal(got, []string{"k8s", "Urgent"}) {
		t.Errorf("card labels = %v, want [k8s Urgent]", got)
	}
	var onBoard []string
	for _, l := range o.get(b).Columns[0].Cards[0].Labels {
		onBoard = append(onBoard, l.Name)
	}
	if !slices.Equal(onBoard, []string{"k8s", "Urgent"}) {
		t.Errorf("board shows card labels %v, want [k8s Urgent]", onBoard)
	}

	if rec := o.do(http.MethodDelete, cardLabelPath(b, k, urgent), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("remove: status = %d, body %s", rec.Code, rec.Body)
	}
	if got := o.cardLabels(b, k); !slices.Equal(got, []string{"k8s"}) {
		t.Errorf("card labels after remove = %v, want [k8s]", got)
	}

	// Deleting a label takes it off its cards.
	o.do(http.MethodDelete, labelsPath(b)+"/"+k8s.ID, nil)
	if got := o.cardLabels(b, k); len(got) != 0 {
		t.Errorf("card labels after deleting the label = %v, want none", got)
	}
}

func TestALabelFromAnotherBoardCannotBeApplied(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	k := o.createCard(b, b.Columns[0], "Upgrade k3s")
	foreign := o.addLabel(o.create("Job hunt"), "Urgent")

	refusal(t, o.do(http.MethodPut, cardLabelPath(b, k, foreign), nil), http.StatusNotFound, "label not found")
	if got := o.cardLabels(b, k); len(got) != 0 {
		t.Errorf("card labels = %v, want none", got)
	}
}

func TestLabelsArePrivateAndTargetedReadsCannotChangeThem(t *testing.T) {
	alice, aliceID := newOwner(t)
	b := alice.create("Homelab")
	k := alice.createCard(b, b.Columns[0], "Mine")
	l := alice.addLabel(b, "Urgent")

	bob, _ := newOwner(t)
	if rec := bob.do(http.MethodGet, labelsPath(b), nil); rec.Code != http.StatusNotFound {
		t.Errorf("bob lists: status = %d, want 404", rec.Code)
	}
	admin := as(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"admin"}, ActAs: aliceID})
	if got := admin.labelNames(b); !slices.Equal(got, []string{"Urgent"}) {
		t.Errorf("targeted read = %v, want [Urgent]", got)
	}
	for _, o := range []owner{bob, admin} {
		if rec := o.do(http.MethodPost, labelsPath(b), map[string]string{"name": "X"}); rec.Code != http.StatusNotFound {
			t.Errorf("create: status = %d, want 404", rec.Code)
		}
		if rec := o.do(http.MethodPatch, labelsPath(b)+"/"+l.ID, map[string]string{"name": "X"}); rec.Code != http.StatusNotFound {
			t.Errorf("rename: status = %d, want 404", rec.Code)
		}
		if rec := o.do(http.MethodPut, cardLabelPath(b, k, l), nil); rec.Code != http.StatusNotFound {
			t.Errorf("apply: status = %d, want 404", rec.Code)
		}
		if rec := o.do(http.MethodDelete, labelsPath(b)+"/"+l.ID, nil); rec.Code != http.StatusNotFound {
			t.Errorf("delete: status = %d, want 404", rec.Code)
		}
	}
}

func TestDeletingALabelledCardTakesItsLabellingWithIt(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Homelab")
	k := o.createCard(b, b.Columns[0], "Upgrade k3s")
	l := o.addLabel(b, "k8s")
	o.do(http.MethodPut, cardLabelPath(b, k, l), nil)

	if rec := o.do(http.MethodDelete, cardPath(b, k), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete labelled card: status = %d, body %s", rec.Code, rec.Body)
	}
	// The label itself stays on the board.
	if got := o.labelNames(b); !slices.Equal(got, []string{"k8s"}) {
		t.Errorf("labels = %v, want [k8s] kept", got)
	}
	if rec := o.do(http.MethodDelete, "/api/coreban/boards/"+b.ID, nil); rec.Code != http.StatusNoContent {
		t.Errorf("delete board with labels: status = %d, body %s", rec.Code, rec.Body)
	}
}
