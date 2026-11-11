package settings

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ejsadiarin/coreban/internal/helper"
)

// maxSettingsBody bounds a settings request: a topic name and a zone name.
const maxSettingsBody = 4 << 10

// Handler serves the owner's settings. Reads resolve the owner through a
// targeted read (act_as); writes always bind to the caller.
type Handler struct {
	Store *Store
	// Changed, when set, is told the owner saved settings, so the reminder
	// dispatcher can wake for reminders a new target unblocks.
	Changed func()
}

// Mount registers the settings routes on r.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/", h.get)
	r.Put("/", h.put)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.ResolveTargetUser(w, r)
	if !ok {
		return
	}
	v, err := h.Store.Get(r.Context(), owner)
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusInternalServerError, "failed to fetch settings")
		return
	}
	helper.RespondJSON(w, http.StatusOK, v)
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	var req Settings
	r.Body = http.MaxBytesReader(w, r.Body, maxSettingsBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		helper.RespondErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	v, err := normalize(req)
	if err != nil {
		helper.RespondErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	switch err := h.Store.Put(r.Context(), owner, v); {
	case errors.Is(err, ErrTopicTaken):
		helper.RespondErrorJSON(w, http.StatusConflict, "ntfy_topic is already in use; choose another name")
		return
	case err != nil:
		helper.RespondErrorJSON(w, http.StatusInternalServerError, "failed to save settings")
		return
	}
	if h.Changed != nil {
		h.Changed()
	}
	v.Saved = true
	helper.RespondJSON(w, http.StatusOK, v)
}
