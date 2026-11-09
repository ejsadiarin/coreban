package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/helper"
)

// Routes builds the HTTP router. Paths match what the gateway forwards
// unchanged: everything lives under /api/coreban, except the
// unauthenticated liveness check at /health.
func (s *Server) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(requestIDMiddleware)
	r.Use(slogMiddleware)
	r.Use(middleware.Recoverer)

	r.Get("/health", s.liveness)

	r.Route("/api/coreban", func(r chi.Router) {
		r.Use(s.authenticate)
		r.Get("/health", s.readiness)
	})

	return r
}

// liveness answers probes without a token and without touching the
// database.
func (s *Server) liveness(w http.ResponseWriter, _ *http.Request) {
	helper.RespondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readiness is the verified health check the gateway runbook calls through
// the proxy: it proves the whole chain (gateway token accepted, database
// reachable) and echoes the owner the request resolves to.
func (s *Server) readiness(w http.ResponseWriter, r *http.Request) {
	owner, ok := helper.ResolveTargetUser(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Pool.Ping(ctx); err != nil {
		slog.Error("readiness: database unreachable", "error", err)
		helper.RespondErrorJSON(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	helper.RespondJSON(w, http.StatusOK, map[string]string{"status": "ok", "owner": owner.String()})
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = r.Header.Get("X-Correlation-ID")
		}
		if id == "" {
			id = uuid.New().String()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}

func slogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"request_id", w.Header().Get("X-Request-ID"),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
