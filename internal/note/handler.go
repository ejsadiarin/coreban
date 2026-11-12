package note

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/attachment"
	"github.com/ejsadiarin/coreban/internal/auth"
	"github.com/ejsadiarin/coreban/internal/feedlink"
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
	caller, _ := auth.UserIDFromContext(r.Context())
	p, err := h.Store.Feed(r.Context(), owner, before, size, owner == caller)
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusInternalServerError, "failed to load the feed")
		return
	}
	helper.RespondJSON(w, http.StatusOK, p)
}

// postRequest posts plain text, attachments the web has uploaded (with
// optional text as their caption), or links an expense or income the web
// just created in corefinance. Reminders and cards get their note when
// they are created ("note": "new").
type postRequest struct {
	Text        string       `json:"text"`
	Attachments *[]string    `json:"attachments"`
	Link        *linkRequest `json:"link"`
}

type linkRequest struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
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
	if req.Link != nil {
		if req.Attachments != nil {
			helper.RespondErrorJSON(w, http.StatusBadRequest, "a linked note takes no attachments")
			return
		}
		h.postRecord(w, r, owner, req)
		return
	}
	uploads, ok := uploadIDs(w, req.Attachments)
	if !ok {
		return
	}
	// Blank text is a note of attachments alone; the store refuses it
	// with none.
	text, err := CleanText(req.Text)
	if err != nil && !errors.Is(err, errBlank) {
		helper.RespondErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := h.Store.Post(r.Context(), owner, text, uploads)
	if !respondStoreError(w, err, "failed to post note") {
		helper.RespondJSON(w, http.StatusCreated, n)
	}
}

// uploadIDs reads the attachments a note names: distinct ids, at most
// attachment.MaxPerNote. None named (nil) is a text-only note.
func uploadIDs(w http.ResponseWriter, named *[]string) ([]uuid.UUID, bool) {
	if named == nil {
		return nil, true
	}
	if len(*named) == 0 || len(*named) > attachment.MaxPerNote {
		helper.RespondErrorJSON(w, http.StatusBadRequest,
			"attachments must name 1 to "+strconv.Itoa(attachment.MaxPerNote)+" uploads")
		return nil, false
	}
	ids := make([]uuid.UUID, len(*named))
	seen := map[uuid.UUID]bool{}
	for i, s := range *named {
		id, err := uuid.Parse(s)
		if err != nil || seen[id] {
			helper.RespondErrorJSON(w, http.StatusBadRequest, "attachments must be distinct upload ids")
			return nil, false
		}
		seen[id] = true
		ids[i] = id
	}
	return ids, true
}

func (h *Handler) postRecord(w http.ResponseWriter, r *http.Request, owner uuid.UUID, req postRequest) {
	if req.Text != "" {
		helper.RespondErrorJSON(w, http.StatusBadRequest, "a note has text or a link, not both")
		return
	}
	if !feedlink.CrossService(req.Link.Kind) {
		helper.RespondErrorJSON(w, http.StatusBadRequest,
			`link kind must be "expense" or "income"; reminders and cards get their note as they are created`)
		return
	}
	id, err := uuid.Parse(req.Link.ID)
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusBadRequest, "link id must be the record's id")
		return
	}
	n, err := h.Store.PostRecord(r.Context(), owner, req.Link.Kind, id)
	if !respondStoreError(w, err, "failed to post note") {
		helper.RespondJSON(w, http.StatusCreated, n)
	}
}

type editRequest struct {
	Text     *string         `json:"text"`
	Archived *bool           `json:"archived"`
	Snapshot json.RawMessage `json:"snapshot"`
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
	if req.Snapshot != nil {
		if s := bytes.TrimSpace(req.Snapshot); len(s) == 0 || s[0] != '{' {
			helper.RespondErrorJSON(w, http.StatusBadRequest, "snapshot must be the record as a JSON object")
			return
		}
		e.Snapshot = req.Snapshot
	}
	if req.Text != nil {
		// Blank clears a caption; the store refuses it on a note with
		// nothing else to show.
		text, err := CleanText(*req.Text)
		if err != nil && !errors.Is(err, errBlank) {
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
	case errors.Is(err, ErrLinked):
		helper.RespondErrorJSON(w, http.StatusConflict, "the note shows a linked record; edit the record instead")
	case errors.Is(err, ErrRecordTaken):
		helper.RespondErrorJSON(w, http.StatusConflict, "that record already has a note")
	case errors.Is(err, ErrTextRequired), errors.Is(err, ErrNotYourUpload):
		helper.RespondErrorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrNotUploaded):
		helper.RespondErrorJSON(w, http.StatusConflict, "an attachment hasn't finished uploading; try again once it has")
	case errors.Is(err, attachment.ErrNoBucket):
		helper.RespondErrorJSON(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, ErrTakesNoSnapshot):
		helper.RespondErrorJSON(w, http.StatusConflict, "only an expense or income note takes a snapshot")
	default:
		helper.RespondErrorJSON(w, http.StatusInternalServerError, msg)
	}
	return true
}
