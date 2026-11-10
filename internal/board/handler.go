package board

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/helper"
	"github.com/ejsadiarin/coreban/internal/settings"
)

// Handler serves the boards API. Reads resolve the owner through a
// targeted read (act_as); writes always bind to the caller.
type Handler struct {
	Store *Store
	// Settings gives each owner's timezone, which card dues are read and
	// shown in.
	Settings *settings.Store
}

// zone is the owner's timezone.
func (h *Handler) zone(ctx context.Context, owner uuid.UUID) (*time.Location, error) {
	s, err := h.Settings.Get(ctx, owner)
	if err != nil {
		return nil, err
	}
	return time.LoadLocation(s.Timezone)
}

// zoned is a response holding times to show in the owner's timezone.
type zoned[T any] interface {
	inZone(loc *time.Location) T
}

// respondZoned writes v with its times in the owner's timezone.
func respondZoned[T zoned[T]](h *Handler, w http.ResponseWriter, r *http.Request, owner uuid.UUID, status int, v T) {
	loc, err := h.zone(r.Context(), owner)
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusInternalServerError, "failed to load your timezone")
		return
	}
	helper.RespondJSON(w, status, v.inZone(loc))
}

// Mount registers the boards routes on r.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Patch("/{id}", h.rename)
	r.Delete("/{id}", h.remove)
	r.Route("/{id}/cards", h.mountCards)
	r.Route("/{id}/columns", h.mountColumns)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.ResolveTargetUser(w, r)
	if !ok {
		return
	}
	boards, err := h.Store.List(r.Context(), owner)
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusInternalServerError, "failed to list boards")
		return
	}
	helper.RespondJSON(w, http.StatusOK, boards)
}

type nameRequest struct {
	Name string `json:"name"`
}

// decodeName reads {"name": …} and writes 400 unless it holds a non-blank
// name.
func decodeName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req nameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		helper.RespondErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return "", false
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		helper.RespondErrorJSON(w, http.StatusBadRequest, "name is required")
		return "", false
	}
	return name, true
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	b, err := h.Store.Create(r.Context(), owner, name)
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusInternalServerError, "failed to create board")
		return
	}
	helper.RespondJSON(w, http.StatusCreated, b)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.ResolveTargetUser(w, r)
	if !ok {
		return
	}
	id, ok := boardID(w, r)
	if !ok {
		return
	}
	b, err := h.Store.Get(r.Context(), owner, id)
	if !respondStoreError(w, err, "failed to fetch board") {
		respondZoned(h, w, r, owner, http.StatusOK, b)
	}
}

func (h *Handler) rename(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	id, ok := boardID(w, r)
	if !ok {
		return
	}
	name, ok := decodeName(w, r)
	if !ok {
		return
	}
	b, err := h.Store.Rename(r.Context(), owner, id, name)
	if !respondStoreError(w, err, "failed to rename board") {
		helper.RespondJSON(w, http.StatusOK, b)
	}
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	id, ok := boardID(w, r)
	if !ok {
		return
	}
	if !respondStoreError(w, h.Store.Delete(r.Context(), owner, id), "failed to delete board") {
		w.WriteHeader(http.StatusNoContent)
	}
}

// boardID parses the {id} URL param. Malformed IDs answer 404, like any
// board the owner can't see.
func boardID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusNotFound, "board not found")
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
		helper.RespondErrorJSON(w, http.StatusNotFound, "board not found")
	default:
		helper.RespondErrorJSON(w, http.StatusInternalServerError, msg)
	}
	return true
}
