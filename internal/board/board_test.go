package board_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

type column struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Cards  []card `json:"cards"`
}

type board struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Columns []column `json:"columns"`
}

// owner is one caller driving the API with a fixed injected identity.
type owner struct {
	t *testing.T
	h http.Handler
}

func as(t *testing.T, id testharness.Identity) owner {
	return owner{t: t, h: testharness.Router(t, id)}
}

func newOwner(t *testing.T) (owner, uuid.UUID) {
	id := uuid.New()
	return as(t, testharness.Identity{Owner: id, Scopes: []string{"coreban:read", "coreban:write"}}), id
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

func (o owner) create(name string) board {
	o.t.Helper()
	rec := o.do(http.MethodPost, "/api/coreban/boards", map[string]string{"name": name})
	if rec.Code != http.StatusCreated {
		o.t.Fatalf("create %q: status = %d, body %s", name, rec.Code, rec.Body)
	}
	return decode[board](o.t, rec)
}

func (o owner) list() []board {
	o.t.Helper()
	rec := o.do(http.MethodGet, "/api/coreban/boards", nil)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("list: status = %d, body %s", rec.Code, rec.Body)
	}
	return decode[[]board](o.t, rec)
}

func names(bs []board) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Name)
	}
	return out
}

func TestNewBoardStartsWithTodoDoingDoneColumns(t *testing.T) {
	o, _ := newOwner(t)
	created := o.create("Homelab")

	rec := o.do(http.MethodGet, "/api/coreban/boards/"+created.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d, body %s", rec.Code, rec.Body)
	}
	got := decode[board](t, rec)
	if got.Name != "Homelab" {
		t.Errorf("name = %q, want Homelab", got.Name)
	}
	want := []column{{Name: "Todo", Status: "todo"}, {Name: "Doing", Status: "in_progress"}, {Name: "Done", Status: "done"}}
	if len(got.Columns) != len(want) {
		t.Fatalf("columns = %+v, want %+v", got.Columns, want)
	}
	for i, c := range got.Columns {
		if c.Name != want[i].Name || c.Status != want[i].Status || c.ID == "" {
			t.Errorf("column %d = %+v, want name %q status %q with an id", i, c, want[i].Name, want[i].Status)
		}
	}
	if len(created.Columns) != 3 {
		t.Errorf("create response columns = %d, want 3", len(created.Columns))
	}
}

func TestOwnerCreatesListsRenamesAndDeletesBoards(t *testing.T) {
	o, _ := newOwner(t)
	if got := o.list(); len(got) != 0 {
		t.Fatalf("fresh owner lists %v, want none", names(got))
	}
	homelab := o.create("Homelab")
	o.create("Job hunt")
	if got := names(o.list()); len(got) != 2 || got[0] != "Homelab" || got[1] != "Job hunt" {
		t.Fatalf("list = %v, want [Homelab Job hunt] in creation order", got)
	}

	rec := o.do(http.MethodPatch, "/api/coreban/boards/"+homelab.ID, map[string]string{"name": "  Homelab ops "})
	if rec.Code != http.StatusOK || decode[board](t, rec).Name != "Homelab ops" {
		t.Fatalf("rename: status = %d, body %s; want 200 named \"Homelab ops\"", rec.Code, rec.Body)
	}

	if rec := o.do(http.MethodDelete, "/api/coreban/boards/"+homelab.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete (board with columns): status = %d, body %s; want 204", rec.Code, rec.Body)
	}
	if rec := o.do(http.MethodGet, "/api/coreban/boards/"+homelab.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("get deleted: status = %d, want 404", rec.Code)
	}
	if got := names(o.list()); len(got) != 1 || got[0] != "Job hunt" {
		t.Errorf("list after delete = %v, want [Job hunt]", got)
	}
}

func TestBlankOrMissingNamesAreRejected(t *testing.T) {
	o, _ := newOwner(t)
	b := o.create("Daily")
	for _, body := range []any{map[string]string{"name": "   "}, map[string]string{}, "not an object"} {
		if rec := o.do(http.MethodPost, "/api/coreban/boards", body); rec.Code != http.StatusBadRequest {
			t.Errorf("create %v: status = %d, want 400", body, rec.Code)
		}
		if rec := o.do(http.MethodPatch, "/api/coreban/boards/"+b.ID, body); rec.Code != http.StatusBadRequest {
			t.Errorf("rename %v: status = %d, want 400", body, rec.Code)
		}
	}
	if got := names(o.list()); len(got) != 1 || got[0] != "Daily" {
		t.Errorf("list = %v, want only the unchanged [Daily]", got)
	}
}

func TestBoardsArePrivateToTheirOwner(t *testing.T) {
	alice, _ := newOwner(t)
	bob, _ := newOwner(t)
	secret := alice.create("Alice only")

	if got := bob.list(); len(got) != 0 {
		t.Errorf("bob lists %v, want none", names(got))
	}
	path := "/api/coreban/boards/" + secret.ID
	for _, req := range []struct {
		method string
		body   any
	}{{http.MethodGet, nil}, {http.MethodPatch, map[string]string{"name": "pwned"}}, {http.MethodDelete, nil}} {
		if rec := bob.do(req.method, path, req.body); rec.Code != http.StatusNotFound {
			t.Errorf("bob %s alice's board: status = %d, want 404", req.method, rec.Code)
		}
	}
	if rec := bob.do(http.MethodGet, "/api/coreban/boards/not-a-uuid", nil); rec.Code != http.StatusNotFound {
		t.Errorf("malformed id: status = %d, want 404", rec.Code)
	}
	if got := names(alice.list()); len(got) != 1 || got[0] != "Alice only" {
		t.Errorf("alice's boards after bob's attempts = %v, want [Alice only]", got)
	}
}

func TestTargetedReadsHonourActAsButWritesBindToTheCaller(t *testing.T) {
	target, targetID := newOwner(t)
	targetBoard := target.create("Target's board")
	adminID := uuid.New()
	admin := as(t, testharness.Identity{Owner: adminID, Scopes: []string{"admin"}, ActAs: targetID})

	if got := names(admin.list()); len(got) != 1 || got[0] != "Target's board" {
		t.Errorf("admin targeted list = %v, want [Target's board]", got)
	}
	if rec := admin.do(http.MethodGet, "/api/coreban/boards/"+targetBoard.ID, nil); rec.Code != http.StatusOK {
		t.Errorf("admin targeted get: status = %d, want 200", rec.Code)
	}

	admin.create("Admin's own")
	if rec := admin.do(http.MethodPatch, "/api/coreban/boards/"+targetBoard.ID, map[string]string{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Errorf("admin rename of target's board: status = %d, want 404 (writes bind to caller)", rec.Code)
	}
	if rec := admin.do(http.MethodDelete, "/api/coreban/boards/"+targetBoard.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("admin delete of target's board: status = %d, want 404 (writes bind to caller)", rec.Code)
	}
	if got := names(target.list()); len(got) != 1 || got[0] != "Target's board" {
		t.Errorf("target's boards = %v, want only the untouched [Target's board]", got)
	}
	adminSelf := as(t, testharness.Identity{Owner: adminID, Scopes: []string{"admin"}})
	if got := names(adminSelf.list()); len(got) != 1 || got[0] != "Admin's own" {
		t.Errorf("admin's own boards = %v, want [Admin's own]", got)
	}

	nonAdmin := as(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"coreban:read"}, ActAs: targetID})
	if got := nonAdmin.list(); len(got) != 0 {
		t.Errorf("non-admin with act_as lists %v, want none (act_as needs admin)", names(got))
	}
}

func TestBoardsRequireAnIdentity(t *testing.T) {
	anon := as(t, testharness.Identity{})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if rec := anon.do(method, "/api/coreban/boards", map[string]string{"name": "x"}); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without identity: status = %d, want 401", method, rec.Code)
		}
	}
}
