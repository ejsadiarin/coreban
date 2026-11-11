package note

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/helper"
)

// maxNoteBody bounds a note request: one note's text, with room for JSON
// escaping.
const maxNoteBody = 64 << 10

// Handler serves the owner's feed. Reads resolve the owner through a
// targeted read (act_as); writes always bind to the caller.
type Handler struct {
	Store *Store
}

// Mount registers the note routes on r.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/", h.feed)
	r.Post("/", h.post)
	r.Patch("/{id}", h.edit)
	r.Delete("/{id}", h.remove)
}

// feed answers one page of the feed: ?limit= notes (default
// DefaultPageSize, at most MaxPageSize) before the ?before= cursor.
func (h *Handler) feed(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.ResolveTargetUser(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	size := DefaultPageSize
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			helper.RespondErrorJSON(w, http.StatusBadRequest, "limit must be a positive number")
			return
		}
		size = min(n, MaxPageSize)
	}
	var before *Cursor
	if v := q.Get("before"); v != "" {
		c, err := ParseCursor(v)
		if err != nil {
			helper.RespondErrorJSON(w, http.StatusBadRequest, "invalid before cursor")
			return
		}
		before = &c
	}
	p, err := h.Store.Feed(r.Context(), owner, before, size)
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusInternalServerError, "failed to load the feed")
		return
	}
	helper.RespondJSON(w, http.StatusOK, p)
}

type postRequest struct {
	Text string `json:"text"`
}

func (h *Handler) post(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	var req postRequest
	if !decodeBody(w, r, &req) {
		return
	}
	text, err := CleanText(req.Text)
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := h.Store.Post(r.Context(), owner, text)
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusInternalServerError, "failed to post note")
		return
	}
	helper.RespondJSON(w, http.StatusCreated, n)
}

type editRequest struct {
	Text     *string `json:"text"`
	Archived *bool   `json:"archived"`
}

func (h *Handler) edit(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	var req editRequest
	if !decodeBody(w, r, &req) {
		return
	}
	e := Edit{Archived: req.Archived}
	if req.Text != nil {
		text, err := CleanText(*req.Text)
		if err != nil {
			helper.RespondErrorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		e.Text = &text
	}
	n, err := h.Store.Edit(r.Context(), owner, id, e)
	if !respondStoreError(w, err, "failed to edit note") {
		helper.RespondJSON(w, http.StatusOK, n)
	}
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	if !respondStoreError(w, h.Store.Delete(r.Context(), owner, id), "failed to delete note") {
		w.WriteHeader(http.StatusNoContent)
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxNoteBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		helper.RespondErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// noteID parses {id}; a malformed one answers 404 like any note the owner
// can't see.
func noteID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusNotFound, "note not found")
		return uuid.Nil, false
	}
	return id, true
}

// respondStoreError writes the response for a non-nil store error and
// reports whether it did.
func respondStoreError(w http.ResponseWriter, err error, msg string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrNotFound):
		helper.RespondErrorJSON(w, http.StatusNotFound, "note not found")
	case errors.Is(err, ErrArchived):
		helper.RespondErrorJSON(w, http.StatusConflict, "the note is archived; restore it first")
	default:
		helper.RespondErrorJSON(w, http.StatusInternalServerError, msg)
	}
	return true
}
