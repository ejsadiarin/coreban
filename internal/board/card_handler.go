package board

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/helper"
)

// maxBoardRequestBody bounds board, column and card request bodies; a
// card's markdown description is the largest thing they carry.
const maxBoardRequestBody = 256 << 10

// mountCards registers the card routes under /boards/{id}/cards.
func (h *Handler) mountCards(r chi.Router) {
	r.Post("/", h.createCard)
	r.Get("/{cardID}", h.getCard)
	r.Patch("/{cardID}", h.editCard)
	r.Post("/{cardID}/move", h.moveCard)
}

type createCardRequest struct {
	ColumnID uuid.UUID `json:"column_id"`
	Title    string    `json:"title"`
}

// decodeBody reads a bounded JSON body into v, writing 400 on failure.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBoardRequestBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		helper.RespondErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func (h *Handler) createCard(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	boardID, ok := boardID(w, r)
	if !ok {
		return
	}
	var req createCardRequest
	if !decodeBody(w, r, &req) {
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		helper.RespondErrorJSON(w, http.StatusBadRequest, "title is required")
		return
	}
	c, err := h.Store.CreateCard(r.Context(), owner, boardID, req.ColumnID, title)
	if !respondCardError(w, err, "failed to create card") {
		helper.RespondJSON(w, http.StatusCreated, c)
	}
}

func (h *Handler) getCard(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.ResolveTargetUser(w, r)
	if !ok {
		return
	}
	boardID, cardID, ok := cardIDs(w, r)
	if !ok {
		return
	}
	c, err := h.Store.GetCard(r.Context(), owner, boardID, cardID)
	if !respondCardError(w, err, "failed to fetch card") {
		helper.RespondJSON(w, http.StatusOK, c)
	}
}

// editCardRequest is a partial edit: absent fields are left unchanged.
type editCardRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
}

func (h *Handler) editCard(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	boardID, cardID, ok := cardIDs(w, r)
	if !ok {
		return
	}
	var req editCardRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			helper.RespondErrorJSON(w, http.StatusBadRequest, "title cannot be blank")
			return
		}
		req.Title = &title
	}
	c, err := h.Store.EditCard(r.Context(), owner, boardID, cardID, CardEdit{Title: req.Title, Description: req.Description})
	if !respondCardError(w, err, "failed to edit card") {
		helper.RespondJSON(w, http.StatusOK, c)
	}
}

// moveCardRequest names the destination column and the slot among that
// column's other cards, top first.
type moveCardRequest struct {
	ColumnID uuid.UUID `json:"column_id"`
	Index    int       `json:"index"`
}

func (h *Handler) moveCard(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	boardID, cardID, ok := cardIDs(w, r)
	if !ok {
		return
	}
	var req moveCardRequest
	if !decodeBody(w, r, &req) {
		return
	}
	c, err := h.Store.MoveCard(r.Context(), owner, boardID, cardID, req.ColumnID, req.Index)
	if !respondCardError(w, err, "failed to move card") {
		helper.RespondJSON(w, http.StatusOK, c)
	}
}

// cardIDs parses the {id} and {cardID} URL params. A malformed card ID
// answers 404, like any card the owner can't see.
func cardIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	boardID, ok := boardID(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	cardID, err := uuid.Parse(chi.URLParam(r, "cardID"))
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusNotFound, "card not found")
		return uuid.Nil, uuid.Nil, false
	}
	return boardID, cardID, true
}

// respondCardError writes the response for a non-nil card store error and
// reports whether it did.
func respondCardError(w http.ResponseWriter, err error, msg string) bool {
	switch {
	case errors.Is(err, ErrCardNotFound):
		helper.RespondErrorJSON(w, http.StatusNotFound, "card not found")
	case errors.Is(err, ErrColumnNotFound):
		helper.RespondErrorJSON(w, http.StatusBadRequest, "column_id is not a column on this board")
	default:
		return respondStoreError(w, err, msg)
	}
	return true
}
