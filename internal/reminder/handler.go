package reminder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/helper"
	"github.com/ejsadiarin/coreban/internal/settings"
)

// maxReminderBody bounds a reminder request: a message and a schedule.
const maxReminderBody = 16 << 10

// Handler serves the owner's reminders. Reads resolve the owner through a
// targeted read (act_as); writes always bind to the caller.
type Handler struct {
	Store    *Store
	Settings *settings.Store
	// Cap is how many reminders one owner may have.
	Cap int
	// Now is the clock that decides whether a moment is in the future.
	Now func() time.Time
	// Changed, when set, is told a reminder was created or rescheduled, so
	// the dispatcher can wake for it.
	Changed func()
}

// Mount registers the reminder routes on r.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Patch("/{id}", h.edit)
	r.Delete("/{id}", h.remove)
}

func (h *Handler) changed() {
	if h.Changed != nil {
		h.Changed()
	}
}

// ownerView loads how the owner sees their reminders: their timezone,
// and whether they have a notification target.
func (h *Handler) ownerView(ctx context.Context, owner uuid.UUID) (view, error) {
	s, err := h.Settings.Get(ctx, owner)
	if err != nil {
		return view{}, err
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return view{}, fmt.Errorf("owner timezone %q: %w", s.Timezone, err)
	}
	return view{loc: loc, noTarget: s.NtfyTopic == nil, now: h.Now()}, nil
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxReminderBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		helper.RespondErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// reminderID parses {id}; a malformed one answers 404 like any reminder
// the owner can't see.
func reminderID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusNotFound, "reminder not found")
		return uuid.Nil, false
	}
	return id, true
}

// respondError writes the response for a non-nil error and reports
// whether it did.
func (h *Handler) respondError(w http.ResponseWriter, err error, msg string) bool {
	var invalid errInvalid
	switch {
	case err == nil:
		return false
	case errors.As(err, &invalid):
		helper.RespondErrorJSON(w, http.StatusBadRequest, invalid.Error())
	case errors.Is(err, ErrNotFound):
		helper.RespondErrorJSON(w, http.StatusNotFound, "reminder not found")
	case errors.Is(err, ErrCapReached):
		helper.RespondErrorJSON(w, http.StatusConflict,
			fmt.Sprintf("you already have %d reminders, the most one owner may have; delete one first", h.Cap))
	default:
		helper.RespondErrorJSON(w, http.StatusInternalServerError, msg)
	}
	return true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.ResolveTargetUser(w, r)
	if !ok {
		return
	}
	v, err := h.ownerView(r.Context(), owner)
	if h.respondError(w, err, "failed to list reminders") {
		return
	}
	var cardID *uuid.UUID
	if raw := r.URL.Query().Get("card_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			helper.RespondErrorJSON(w, http.StatusBadRequest, "card_id must be a card's id")
			return
		}
		cardID = &id
	}
	out, err := h.Store.List(r.Context(), owner, cardID, v)
	if !h.respondError(w, err, "failed to list reminders") {
		helper.RespondJSON(w, http.StatusOK, out)
	}
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.ResolveTargetUser(w, r)
	if !ok {
		return
	}
	id, ok := reminderID(w, r)
	if !ok {
		return
	}
	h.respondReminder(w, r, owner, id, http.StatusOK, "failed to fetch reminder")
}

// respondReminder writes one reminder as the owner sees it now.
func (h *Handler) respondReminder(w http.ResponseWriter, r *http.Request, owner, id uuid.UUID, status int, msg string) {
	v, err := h.ownerView(r.Context(), owner)
	if h.respondError(w, err, msg) {
		return
	}
	out, err := h.Store.Get(r.Context(), owner, id, v)
	if !h.respondError(w, err, msg) {
		helper.RespondJSON(w, status, out)
	}
}

// createRequest is a new reminder. With a card_id, the message defaults to
// the card's title and the schedule may be {"kind": "due"}: once, at the
// card's due.
type createRequest struct {
	Message  string          `json:"message"`
	Schedule scheduleRequest `json:"schedule"`
	CardID   *string         `json:"card_id"`
}

// cardOf resolves a requested card_id to one of the owner's cards.
func (h *Handler) cardOf(ctx context.Context, owner uuid.UUID, raw string) (*card, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, errNotYourCard
	}
	c, err := h.Store.ownedCard(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// scheduleFor reads a requested schedule; "due" needs the card it's on.
func scheduleFor(req scheduleRequest, c *card, v view) (Schedule, error) {
	if req.Kind == KindDue {
		return atDue(c, v.loc, v.now)
	}
	return parseSchedule(req, v.loc, v.now)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	var req createRequest
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := h.ownerView(r.Context(), owner)
	if h.respondError(w, err, "failed to create reminder") {
		return
	}
	var c *card
	var cardID *uuid.UUID
	if req.CardID != nil {
		if c, err = h.cardOf(r.Context(), owner, *req.CardID); h.respondError(w, err, "failed to create reminder") {
			return
		}
		cardID = &c.ref.ID
	}
	sched, err := scheduleFor(req.Schedule, c, v)
	if h.respondError(w, err, "") {
		return
	}
	message := req.Message
	if strings.TrimSpace(message) == "" && c != nil {
		message = c.ref.Title
	}
	msg, err := normalizeMessage(message)
	if h.respondError(w, err, "") {
		return
	}
	id, err := h.Store.Create(r.Context(), owner, msg, sched, cardID, v, h.Cap)
	if h.respondError(w, err, "failed to create reminder") {
		return
	}
	h.changed()
	h.respondReminder(w, r, owner, id, http.StatusCreated, "failed to fetch reminder")
}

// editRequest is a partial edit: absent fields are left unchanged.
// card_id: null detaches the reminder from its card.
type editRequest struct {
	Message  *string          `json:"message"`
	Schedule *scheduleRequest `json:"schedule"`
	CardID   json.RawMessage  `json:"card_id"`
}

func (h *Handler) edit(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	id, ok := reminderID(w, r)
	if !ok {
		return
	}
	var req editRequest
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := h.ownerView(r.Context(), owner)
	if h.respondError(w, err, "failed to edit reminder") {
		return
	}
	var e Edit
	var c *card
	if len(req.CardID) > 0 {
		e.SetCard = true
		if string(req.CardID) != "null" {
			var raw string
			if err := json.Unmarshal(req.CardID, &raw); err != nil {
				helper.RespondErrorJSON(w, http.StatusBadRequest, "card_id must be a card's id, or null")
				return
			}
			if c, err = h.cardOf(r.Context(), owner, raw); h.respondError(w, err, "failed to edit reminder") {
				return
			}
			e.Card = &c.ref.ID
		}
	}
	if req.Schedule != nil && req.Schedule.Kind == KindDue && !e.SetCard {
		// At the due of the card it's already on.
		current, err := h.Store.Get(r.Context(), owner, id, v)
		if h.respondError(w, err, "failed to edit reminder") {
			return
		}
		if current.Card != nil {
			if c, err = h.cardOf(r.Context(), owner, current.Card.ID.String()); h.respondError(w, err, "failed to edit reminder") {
				return
			}
		}
	}
	if req.Message != nil {
		msg, err := normalizeMessage(*req.Message)
		if h.respondError(w, err, "") {
			return
		}
		e.Message = &msg
	}
	if req.Schedule != nil {
		sched, err := scheduleFor(*req.Schedule, c, v)
		if h.respondError(w, err, "") {
			return
		}
		e.Schedule = &sched
	}
	if h.respondError(w, h.Store.Edit(r.Context(), owner, id, e, v), "failed to edit reminder") {
		return
	}
	if e.Schedule != nil {
		h.changed()
	}
	h.respondReminder(w, r, owner, id, http.StatusOK, "failed to fetch reminder")
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	id, ok := reminderID(w, r)
	if !ok {
		return
	}
	if !h.respondError(w, h.Store.Delete(r.Context(), owner, id), "failed to delete reminder") {
		w.WriteHeader(http.StatusNoContent)
	}
}
