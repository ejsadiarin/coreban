package server

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Server holds the dependencies for the coreban API.
type Server struct {
	Pool *pgxpool.Pool
	// authenticate establishes the verified identity for everything under
	// /api/coreban. In production it is the gateway token verifier's
	// middleware; tests may inject an identity directly.
	authenticate func(http.Handler) http.Handler
}

// New wires a Server. It panics on a nil authenticate: serving the API
// without identity verification is never an option.
func New(pool *pgxpool.Pool, authenticate func(http.Handler) http.Handler) *Server {
	if authenticate == nil {
		panic("server: authenticate middleware is required")
	}
	return &Server{Pool: pool, authenticate: authenticate}
}
