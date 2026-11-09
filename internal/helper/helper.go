package helper

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/auth"
)

func RespondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		if err := json.NewEncoder(w).Encode(data); err != nil {
			slog.Info("failed to encode response", "error", err.Error())
		}
	}
}

func RespondErrorJSON(w http.ResponseWriter, status int, message string) {
	RespondJSON(w, status, map[string]string{"error": message})
}

// GetUserID resolves the verified caller identity. It writes 401 when no
// identity was established and the handler must stop.
func GetUserID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		RespondErrorJSON(w, http.StatusUnauthorized, "valid internal credentials are required")
		return uuid.Nil, false
	}
	return id, true
}

// ResolveTargetUser resolves act_as delegation: an admin-scoped caller with
// a signed act_as claim reads as the target, everyone else reads as
// themselves. The raw query param is never trusted.
func ResolveTargetUser(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if target, ok := auth.ActAsFromContext(r.Context()); ok && auth.HasScope(r.Context(), "admin") {
		return target, true
	}
	return GetUserID(w, r)
}
