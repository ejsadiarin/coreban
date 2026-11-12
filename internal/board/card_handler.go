package board

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/feedlink"
	"github.com/ejsadiarin/coreban/internal/helper"
	"github.com/ejsadiarin/coreban/internal/moment"
)

// maxBoardRequestBody bounds board, column and card request bodies; a
// card's markdown description is the largest thing they carry.
const maxBoardRequestBody = 256 << 10

// mountCards registers the card routes under /boards/{id}/cards.
func (h *Handler) mountCards(r chi.Router) {
	r.Get("/", h.listCards)
	r.Post("/", h.createCard)
	r.Get("/{cardID}", h.getCard)
	r.Patch("/{cardID}", h.editCard)
	r.Delete("/{cardID}", h.deleteCard)
	r.Post("/{cardID}/move", h.moveCard)
	r.Put("/{cardID}/labels/{labelID}", h.setCardLabel(true))
	r.Delete("/{cardID}/labels/{labelID}", h.setCardLabel(false))
}

type createCardRequest struct {
	ColumnID uuid.UUID `json:"column_id"`
	Title    string    `json:"title"`
	// Note, when set, gives the card a note in the Feed: "new" adds one,
	// a plain note's id converts that note (ADR-0003).
	Note *string `json:"note"`
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
	link, err := feedlink.Parse(req.Note)
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	c, err := h.Store.CreateCard(r.Context(), owner, boardID, req.ColumnID, title, link)
	if !respondCardError(w, err, "failed to create card") {
		respondZoned(h, w, r, owner, http.StatusCreated, c)
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
		respondZoned(h, w, r, owner, http.StatusOK, c)
	}
}

// cardList is a list response of cards.
type cardList []Card

func (l cardList) inZone(loc *time.Location) cardList {
	out := make(cardList, len(l))
	for i, c := range l {
		out[i] = c.inZone(loc)
	}
	return out
}

// listCards lists the board's cards in priority order, or with
// ?archived=true its archived cards, most recently archived first.
func (h *Handler) listCards(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.ResolveTargetUser(w, r)
	if !ok {
		return
	}
	boardID, ok := boardID(w, r)
	if !ok {
		return
	}
	cards, err := h.Store.ListCards(r.Context(), owner, boardID, r.URL.Query().Get("archived") == "true")
	if !respondCardError(w, err, "failed to list cards") {
		respondZoned(h, w, r, owner, http.StatusOK, cardList(cards))
	}
}

func (h *Handler) deleteCard(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	boardID, cardID, ok := cardIDs(w, r)
	if !ok {
		return
	}
	if !respondCardError(w, h.Store.DeleteCard(r.Context(), owner, boardID, cardID), "failed to delete card") {
		w.WriteHeader(http.StatusNoContent)
	}
}

// editCardRequest is a partial edit: absent fields are left unchanged.
// Due is null to clear it, else {"date": "2026-11-14"} or {"at": …} (an
// RFC 3339 instant, or a wall-clock time read in the owner's timezone).
type editCardRequest struct {
	Title       *string         `json:"title"`
	Description *string         `json:"description"`
	Due         json.RawMessage `json:"due"`
	Archived    *bool           `json:"archived"`
}

type dueRequest struct {
	Date *string `json:"date"`
	At   *string `json:"at"`
}

// parseDue reads a requested due for an owner in loc; nil clears it. Its
// error text is the 400 detail.
func parseDue(raw json.RawMessage, loc *time.Location) (*Due, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	var req dueRequest
	if err := json.Unmarshal(raw, &req); err != nil || (req.Date == nil) == (req.At == nil) {
		return nil, errors.New("due must have a date or an at, not both")
	}
	if req.Date != nil {
		d := strings.TrimSpace(*req.Date)
		if _, err := time.Parse(dateLayout, d); err != nil {
			return nil, errors.New("due.date must be a date such as 2026-11-14")
		}
		return &Due{Date: d}, nil
	}
	at, ok := moment.Parse(strings.TrimSpace(*req.At), loc)
	if !ok {
		return nil, errors.New("due.at must be a date and time such as 2026-11-14T17:00")
	}
	return &Due{At: &at}, nil
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
	e := CardEdit{Title: req.Title, Description: req.Description, Archived: req.Archived}
	if len(req.Due) > 0 {
		loc, err := h.zone(r.Context(), owner)
		if err != nil {
			helper.RespondErrorJSON(w, http.StatusInternalServerError, "failed to load your timezone")
			return
		}
		due, err := parseDue(req.Due, loc)
		if err != nil {
			helper.RespondErrorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		e.SetDue, e.Due = true, due
	}
	c, err := h.Store.EditCard(r.Context(), owner, boardID, cardID, e)
	if !respondCardError(w, err, "failed to edit card") {
		respondZoned(h, w, r, owner, http.StatusOK, c)
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
		respondZoned(h, w, r, owner, http.StatusOK, c)
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
	case errors.Is(err, ErrCardArchived):
		helper.RespondErrorJSON(w, http.StatusConflict, "the card is archived; restore it first")
	case errors.Is(err, ErrColumnNotFound):
		helper.RespondErrorJSON(w, http.StatusBadRequest, "column_id is not a column on this board")
	case errors.Is(err, feedlink.ErrNotLinkable):
		helper.RespondErrorJSON(w, http.StatusBadRequest, err.Error())
	default:
		return respondStoreError(w, err, msg)
	}
	return true
}
