package attachment

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/auth"
	"github.com/ejsadiarin/coreban/internal/helper"
)

const maxRequestBody = 4 << 10

// Handler gives the web upload links. Posting a note names the uploads
// (note package); the Feed shows them.
type Handler struct {
	Store *Store
	// Bucket is nil when no bucket is configured: uploads wait (503).
	Bucket *Bucket
	// Quota is how many bytes one owner may store (0: no quota). Admins
	// have none.
	Quota int64
}

// Mount registers the attachment routes on r.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/", h.reserve)
}

func (h *Handler) reserve(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.GetUserID(w, r)
	if !ok {
		return
	}
	if auth.HasScope(r.Context(), "guest") {
		helper.RespondErrorJSON(w, http.StatusForbidden, "demo accounts can't upload attachments")
		return
	}
	if h.Bucket == nil {
		helper.RespondErrorJSON(w, http.StatusServiceUnavailable, ErrNoBucket.Error())
		return
	}
	var req Request
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		helper.RespondErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if refused := req.Check(); refused != nil {
		status := http.StatusBadRequest
		if refused.TooLarge {
			status = http.StatusRequestEntityTooLarge
		}
		helper.RespondErrorJSON(w, status, refused.Reason)
		return
	}
	quota := h.Quota
	if auth.HasScope(r.Context(), "admin") {
		quota = 0
	}
	// Links are signed before the row is kept, so a failure leaves no
	// reservation the web never heard of counting toward the quota.
	res := Reservation{ID: uuid.New(), ExpiresAt: time.Now().Add(UploadLinkTTL).UTC()}
	var err error
	if res.Upload, err = h.Bucket.UploadLink(r.Context(), Key(owner, res.ID), req.ContentType, req.Size); err != nil {
		helper.RespondErrorJSON(w, http.StatusInternalServerError, "failed to sign the upload")
		return
	}
	if req.Poster != nil {
		l, err := h.Bucket.UploadLink(r.Context(), PosterKey(owner, res.ID), PosterContentType, req.Poster.Size)
		if err != nil {
			helper.RespondErrorJSON(w, http.StatusInternalServerError, "failed to sign the upload")
			return
		}
		res.PosterUpload = &l
	}
	_, err = h.Store.Reserve(r.Context(), owner, res.ID, req, quota)
	var over *QuotaError
	switch {
	case errors.As(err, &over):
		helper.RespondErrorJSON(w, http.StatusConflict, over.Error())
	case err != nil:
		helper.RespondErrorJSON(w, http.StatusInternalServerError, "failed to reserve the upload")
	default:
		helper.RespondJSON(w, http.StatusCreated, res)
	}
}
