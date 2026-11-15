package board

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/helper"
)

// mountColumns registers the column routes under /boards/{id}/columns.
func (h *Handler) mountColumns(r chi.Router) {
	r.Post("/", h.addColumn)
	r.Patch("/{columnID}", h.editColumn)
	r.Post("/{columnID}/move", h.moveColumn)
	r.Delete("/{columnID}", h.deleteColumn)
}

type addColumnRequest struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
}

const invalidStatus = "status must be todo, in_progress, or done"

func (h *Handler) addColumn(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	boardID, ok := boardID(w, r)
	if !ok {
		return
	}
	var req addColumnRequest
	if !decodeBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		helper.RespondErrorJSON(w, http.StatusBadRequest, "name is required")
		return
	}
	if !req.Status.Valid() {
		helper.RespondErrorJSON(w, http.StatusBadRequest, invalidStatus)
		return
	}
	c, err := h.Store.AddColumn(r.Context(), owner, boardID, name, req.Status)
	if !respondColumnError(w, err, "failed to add column") {
		helper.RespondJSON(w, http.StatusCreated, c)
	}
}

// editColumnRequest is a partial edit: absent fields are left unchanged.
type editColumnRequest struct {
	Name   *string `json:"name"`
	Status *Status `json:"status"`
}

func (h *Handler) editColumn(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	boardID, columnID, ok := columnIDs(w, r)
	if !ok {
		return
	}
	var req editColumnRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			helper.RespondErrorJSON(w, http.StatusBadRequest, "name cannot be blank")
			return
		}
		req.Name = &name
	}
	if req.Status != nil && !req.Status.Valid() {
		helper.RespondErrorJSON(w, http.StatusBadRequest, invalidStatus)
		return
	}
	c, err := h.Store.EditColumn(r.Context(), owner, boardID, columnID, ColumnEdit{Name: req.Name, Status: req.Status})
	if !respondColumnError(w, err, "failed to edit column") {
		respondZoned(h, w, r, owner, http.StatusOK, c)
	}
}

// moveColumnRequest names the slot among the board's other columns, first
// (leftmost) is 0.
type moveColumnRequest struct {
	Index int `json:"index"`
}

func (h *Handler) moveColumn(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	boardID, columnID, ok := columnIDs(w, r)
	if !ok {
		return
	}
	var req moveColumnRequest
	if !decodeBody(w, r, &req) {
		return
	}
	c, err := h.Store.MoveColumn(r.Context(), owner, boardID, columnID, req.Index)
	if !respondColumnError(w, err, "failed to move column") {
		respondZoned(h, w, r, owner, http.StatusOK, c)
	}
}

func (h *Handler) deleteColumn(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	boardID, columnID, ok := columnIDs(w, r)
	if !ok {
		return
	}
	if !respondColumnError(w, h.Store.DeleteColumn(r.Context(), owner, boardID, columnID), "failed to delete column") {
		w.WriteHeader(http.StatusNoContent)
	}
}

// columnIDs parses the {id} and {columnID} URL params. A malformed column
// ID answers 404, like any column not on the owner's board.
func columnIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	boardID, ok := boardID(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	columnID, err := uuid.Parse(chi.URLParam(r, "columnID"))
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusNotFound, "column not found")
		return uuid.Nil, uuid.Nil, false
	}
	return boardID, columnID, true
}

// respondColumnError writes the response for a non-nil column store error
// and reports whether it did.
func respondColumnError(w http.ResponseWriter, err error, msg string) bool {
	var refused *RefusedError
	switch {
	case errors.As(err, &refused):
		helper.RespondErrorJSON(w, http.StatusConflict, refused.Error())
	case errors.Is(err, ErrColumnNotFound):
		helper.RespondErrorJSON(w, http.StatusNotFound, "column not found")
	default:
		return respondStoreError(w, err, msg)
	}
	return true
}
