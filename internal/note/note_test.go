package note_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

type note struct {
	ID         string  `json:"id"`
	Text       string  `json:"text"`
	ArchivedAt *string `json:"archived_at"`
	CreatedAt  string  `json:"created_at"`
}

type page struct {
	Notes []note  `json:"notes"`
	Older *string `json:"older"`
}

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

func (o owner) post(text string) note {
	o.t.Helper()
	rec := o.do(http.MethodPost, "/api/coreban/notes", map[string]string{"text": text})
	if rec.Code != http.StatusCreated {
		o.t.Fatalf("post %q: status = %d, body %s", text, rec.Code, rec.Body)
	}
	return decode[note](o.t, rec)
}

func (o owner) feed(query string) page {
	o.t.Helper()
	rec := o.do(http.MethodGet, "/api/coreban/notes"+query, nil)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("feed %q: status = %d, body %s", query, rec.Code, rec.Body)
	}
	return decode[page](o.t, rec)
}

func (o owner) patch(id string, body any) *httptest.ResponseRecorder {
	o.t.Helper()
	return o.do(http.MethodPatch, "/api/coreban/notes/"+id, body)
}

func texts(ns []note) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.Text)
	}
	return out
}

func TestPostedNotesReadBackNewestLast(t *testing.T) {
	o, _ := newOwner(t)
	if got := o.feed(""); len(got.Notes) != 0 || got.Older != nil {
		t.Fatalf("fresh owner's feed = %+v, want empty with no older page", got)
	}
	first := o.post("buy milk")
	if first.ID == "" || first.Text != "buy milk" || first.ArchivedAt != nil || first.CreatedAt == "" {
		t.Errorf("posted note = %+v", first)
	}
	o.post("  https://example.com/a  ")
	o.post("call mom")

	got := o.feed("")
	if want := []string{"buy milk", "https://example.com/a", "call mom"}; fmt.Sprint(texts(got.Notes)) != fmt.Sprint(want) {
		t.Errorf("feed = %q, want %q (oldest first, trimmed)", texts(got.Notes), want)
	}
	if got.Older != nil {
		t.Errorf("older = %q, want none on a short feed", *got.Older)
	}
}

func TestBlankOrOverlongNotesAreRejected(t *testing.T) {
	o, _ := newOwner(t)
	for name, body := range map[string]any{
		"empty":      map[string]string{"text": ""},
		"whitespace": map[string]string{"text": " \n\t "},
		"missing":    map[string]string{},
		"too long":   map[string]string{"text": strings.Repeat("é", 4001)},
		"not json":   nil,
	} {
		t.Run(name, func(t *testing.T) {
			if rec := o.do(http.MethodPost, "/api/coreban/notes", body); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body %s)", rec.Code, rec.Body)
			}
		})
	}
	o.post(strings.Repeat("é", 4000))

	n := o.post("keep me")
	for name, body := range map[string]any{
		"blank":    map[string]string{"text": "   "},
		"too long": map[string]string{"text": strings.Repeat("a", 4001)},
	} {
		t.Run("edit "+name, func(t *testing.T) {
			if rec := o.patch(n.ID, body); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body %s)", rec.Code, rec.Body)
			}
		})
	}
}

func TestPagingBackNeverSkipsOrRepeatsANote(t *testing.T) {
	o, _ := newOwner(t)
	var want []string
	for i := range 7 {
		want = append(want, o.post(fmt.Sprintf("note %d", i)).ID)
	}

	var got []string
	query := "?limit=3"
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("paging never ended")
		}
		p := o.feed(query)
		if len(p.Notes) > 3 {
			t.Fatalf("page has %d notes, want at most 3", len(p.Notes))
		}
		ids := make([]string, 0, len(p.Notes))
		for _, n := range p.Notes {
			ids = append(ids, n.ID)
		}
		got = append(ids, got...)
		if p.Older == nil {
			break
		}
		// A note posted mid-scroll lands at the end, never inside the
		// older pages being read.
		if pages == 0 {
			o.post("posted while scrolling")
		}
		query = "?limit=3&before=" + *p.Older
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("paged ids = %v, want %v", got, want)
	}
}

func TestFeedRejectsAMalformedCursorOrLimit(t *testing.T) {
	o, _ := newOwner(t)
	for _, q := range []string{"?before=abc", "?limit=0", "?limit=-1", "?limit=x"} {
		if rec := o.do(http.MethodGet, "/api/coreban/notes"+q, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
	// An oversized limit is capped rather than refused.
	if rec := o.do(http.MethodGet, "/api/coreban/notes?limit=100000", nil); rec.Code != http.StatusOK {
		t.Errorf("big limit: status = %d, want 200", rec.Code)
	}
}

func TestEditingANotesText(t *testing.T) {
	o, _ := newOwner(t)
	n := o.post("buy milk")
	rec := o.patch(n.ID, map[string]string{"text": " buy oat milk "})
	if rec.Code != http.StatusOK {
		t.Fatalf("edit: status = %d, body %s", rec.Code, rec.Body)
	}
	if got := decode[note](t, rec); got.Text != "buy oat milk" || got.ID != n.ID {
		t.Errorf("edited = %+v", got)
	}
	if got := o.feed(""); got.Notes[0].Text != "buy oat milk" {
		t.Errorf("feed after edit = %q", texts(got.Notes))
	}
}

func TestArchivedNotesStayInPlaceFlaggedAndCanBeRestored(t *testing.T) {
	o, _ := newOwner(t)
	o.post("one")
	mid := o.post("two")
	o.post("three")

	rec := o.patch(mid.ID, map[string]bool{"archived": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("archive: status = %d, body %s", rec.Code, rec.Body)
	}
	if got := decode[note](t, rec); got.ArchivedAt == nil {
		t.Errorf("archive response = %+v, want archived_at set", got)
	}
	f := o.feed("")
	if fmt.Sprint(texts(f.Notes)) != "[one two three]" {
		t.Fatalf("feed = %q, want the archived note kept in place", texts(f.Notes))
	}
	if f.Notes[1].ArchivedAt == nil || f.Notes[0].ArchivedAt != nil || f.Notes[2].ArchivedAt != nil {
		t.Errorf("only the middle note should be flagged archived: %+v", f.Notes)
	}

	// An archived note's text can't be edited until it is restored.
	if rec := o.patch(mid.ID, map[string]string{"text": "changed"}); rec.Code != http.StatusConflict {
		t.Errorf("edit archived: status = %d, want 409", rec.Code)
	}

	rec = o.patch(mid.ID, map[string]bool{"archived": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: status = %d, body %s", rec.Code, rec.Body)
	}
	if got := decode[note](t, rec); got.ArchivedAt != nil || got.Text != "two" {
		t.Errorf("restored = %+v", got)
	}
	if f := o.feed(""); f.Notes[1].ArchivedAt != nil {
		t.Errorf("restored note still flagged: %+v", f.Notes[1])
	}
}

func TestPermanentlyDeletedNoteIsGone(t *testing.T) {
	o, _ := newOwner(t)
	keep := o.post("keep")
	gone := o.post("gone")
	if rec := o.do(http.MethodDelete, "/api/coreban/notes/"+gone.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, body %s", rec.Code, rec.Body)
	}
	if f := o.feed(""); len(f.Notes) != 1 || f.Notes[0].ID != keep.ID {
		t.Errorf("feed after delete = %q", texts(f.Notes))
	}
	if rec := o.do(http.MethodDelete, "/api/coreban/notes/"+gone.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("second delete: status = %d, want 404", rec.Code)
	}
	if rec := o.patch(gone.ID, map[string]bool{"archived": true}); rec.Code != http.StatusNotFound {
		t.Errorf("archive deleted: status = %d, want 404", rec.Code)
	}

	// An archived note can be permanently deleted too.
	o.patch(keep.ID, map[string]bool{"archived": true})
	if rec := o.do(http.MethodDelete, "/api/coreban/notes/"+keep.ID, nil); rec.Code != http.StatusNoContent {
		t.Errorf("delete archived: status = %d", rec.Code)
	}
}

func TestOwnersNeverSeeOrTouchEachOthersNotes(t *testing.T) {
	alice, aliceID := newOwner(t)
	bob, _ := newOwner(t)
	n := alice.post("alice's secret")

	if f := bob.feed(""); len(f.Notes) != 0 {
		t.Errorf("bob sees %q", texts(f.Notes))
	}
	if rec := bob.patch(n.ID, map[string]string{"text": "hijack"}); rec.Code != http.StatusNotFound {
		t.Errorf("bob edit: status = %d, want 404", rec.Code)
	}
	if rec := bob.patch(n.ID, map[string]bool{"archived": true}); rec.Code != http.StatusNotFound {
		t.Errorf("bob archive: status = %d, want 404", rec.Code)
	}
	if rec := bob.do(http.MethodDelete, "/api/coreban/notes/"+n.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("bob delete: status = %d, want 404", rec.Code)
	}
	if rec := bob.patch("not-a-uuid", map[string]string{"text": "x"}); rec.Code != http.StatusNotFound {
		t.Errorf("malformed id: status = %d, want 404", rec.Code)
	}

	// A bob cursor can't page into alice's notes either.
	if f := bob.feed("?before=999999999"); len(f.Notes) != 0 {
		t.Errorf("bob paging sees %q", texts(f.Notes))
	}

	// An admin's targeted read sees alice's feed; its writes stay its own.
	admin := as(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"admin"}, ActAs: aliceID})
	if f := admin.feed(""); len(f.Notes) != 1 || f.Notes[0].Text != "alice's secret" {
		t.Errorf("admin targeted read = %q", texts(f.Notes))
	}
	if rec := admin.patch(n.ID, map[string]string{"text": "admin edit"}); rec.Code != http.StatusNotFound {
		t.Errorf("admin write on alice's note: status = %d, want 404", rec.Code)
	}
}

func TestNotesNeedAnIdentity(t *testing.T) {
	anon := as(t, testharness.Identity{})
	if rec := anon.do(http.MethodGet, "/api/coreban/notes", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous feed: status = %d, want 401", rec.Code)
	}
	if rec := anon.do(http.MethodPost, "/api/coreban/notes", map[string]string{"text": "x"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous post: status = %d, want 401", rec.Code)
	}
}
