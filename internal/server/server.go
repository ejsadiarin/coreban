package server

import (
	"database/sql"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

type Server struct {
	sql.Conn
}

// middlewares, register routes,
func NewServer() {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)                 // attaches a unique ID to each request
	r.Use(middleware.Heartbeat("/ping"))        // healthcheck endpoint on /ping
	r.Use(middleware.Logger)                    // logs start and end of each request
	r.Use(middleware.Recoverer)                 // recovers from panics, returns 500
	r.Use(middleware.Compress(5))               // gzip response compression
	r.Use(middleware.Timeout(60 * time.Second)) // cancels ctx after duration
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type"},
	}))
	r.Use(middleware.StripSlashes)
}
