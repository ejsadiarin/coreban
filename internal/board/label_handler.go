package board

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/helper"
)

// mountLabels registers the label routes under /boards/{id}/labels.
func (h *Handler) mountLabels(r chi.Router) {
	r.Get("/", h.listLabels)
	r.Post("/", h.createLabel)
	r.Patch("/{labelID}", h.renameLabel)
	r.Delete("/{labelID}", h.deleteLabel)
}

func (h *Handler) listLabels(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.ResolveTargetUser(w, r)
	if !ok {
		return
	}
	boardID, ok := boardID(w, r)
	if !ok {
		return
	}
	labels, err := h.Store.ListLabels(r.Context(), owner, boardID)
	if !respondLabelError(w, err, "failed to list labels") {
		helper.RespondJSON(w, http.StatusOK, labels)
	}
}

func (h *Handler) createLabel(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	boardID, ok := boardID(w, r)
	if !ok {
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	l, err := h.Store.CreateLabel(r.Context(), owner, boardID, name)
	if !respondLabelError(w, err, "failed to create label") {
		helper.RespondJSON(w, http.StatusCreated, l)
	}
}

func (h *Handler) renameLabel(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	boardID, labelID, ok := labelIDs(w, r)
	if !ok {
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	l, err := h.Store.RenameLabel(r.Context(), owner, boardID, labelID, name)
	if !respondLabelError(w, err, "failed to rename label") {
		helper.RespondJSON(w, http.StatusOK, l)
	}
}

func (h *Handler) deleteLabel(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	boardID, labelID, ok := labelIDs(w, r)
	if !ok {
		return
	}
	if !respondLabelError(w, h.Store.DeleteLabel(r.Context(), owner, boardID, labelID), "failed to delete label") {
		w.WriteHeader(http.StatusNoContent)
	}
}

// setCardLabel returns the handler that applies (on) or removes the
// {labelID} label on the {cardID} card.
func (h *Handler) setCardLabel(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner, ok := helper.GetUserID(w, r)
		if !ok {
			return
		}
		boardID, cardID, ok := cardIDs(w, r)
		if !ok {
			return
		}
		labelID, ok := labelIDParam(w, r)
		if !ok {
			return
		}
		err := h.Store.SetCardLabel(r.Context(), owner, boardID, cardID, labelID, on)
		if !respondLabelError(w, err, "failed to change the card's labels") {
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

// labelIDs parses the {id} and {labelID} URL params. A malformed label ID
// answers 404, like any label not on the owner's board.
func labelIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	boardID, ok := boardID(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	labelID, ok := labelIDParam(w, r)
	return boardID, labelID, ok
}

// labelIDParam parses {labelID}; a malformed one answers 404.
func labelIDParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "labelID"))
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusNotFound, "label not found")
		return uuid.Nil, false
	}
	return id, true
}

// respondLabelError writes the response for a non-nil label store error and
// reports whether it did.
func respondLabelError(w http.ResponseWriter, err error, msg string) bool {
	var taken *LabelTakenError
	switch {
	case errors.As(err, &taken):
		helper.RespondErrorJSON(w, http.StatusConflict, taken.Error())
	case errors.Is(err, ErrLabelNotFound):
		helper.RespondErrorJSON(w, http.StatusNotFound, "label not found")
	default:
		return respondCardError(w, err, msg)
	}
	return true
}
