package server

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ejsadiarin/coreban/internal/attachment"
	"github.com/ejsadiarin/coreban/internal/board"
	"github.com/ejsadiarin/coreban/internal/note"
	"github.com/ejsadiarin/coreban/internal/reminder"
	"github.com/ejsadiarin/coreban/internal/settings"
)

// Server holds the dependencies for the coreban API.
type Server struct {
	Pool *pgxpool.Pool
	// authenticate establishes the verified identity for everything under
	// /api/coreban. In production it is the gateway token verifier's
	// middleware; tests may inject an identity directly.
	authenticate func(http.Handler) http.Handler
	Boards       *board.Handler
	Settings     *settings.Handler
	Reminders    *reminder.Handler
	Notes        *note.Handler
	Attachments  *attachment.Handler
}

// New wires a Server. It panics on a nil authenticate: serving the API
// without identity verification is never an option.
func New(pool *pgxpool.Pool, authenticate func(http.Handler) http.Handler) *Server {
	if authenticate == nil {
		panic("server: authenticate middleware is required")
	}
	settingsStore := &settings.Store{Pool: pool}
	reminders := &reminder.Handler{
		Store:    &reminder.Store{Pool: pool},
		Settings: settingsStore,
		Cap:      reminder.DefaultCap,
		Now:      time.Now,
	}
	return &Server{
		Pool:         pool,
		authenticate: authenticate,
		Boards:       &board.Handler{Store: &board.Store{Pool: pool}, Settings: settingsStore},
		Settings:     &settings.Handler{Store: settingsStore},
		Reminders:    reminders,
		// Linked reminders render as the Reminders page shows them, with
		// the same clock and cap the server is given.
		Notes:       &note.Handler{Store: &note.Store{Pool: pool, Reminders: reminders.Render}},
		Attachments: &attachment.Handler{Store: &attachment.Store{Pool: pool}, Quota: attachment.DefaultQuota},
	}
}

// UseAttachments keeps attachments in b, holding each owner (admins
// aside) to quota bytes (0: none). Without it, uploads answer 503.
func (s *Server) UseAttachments(b *attachment.Bucket, quota int64) {
	s.Attachments.Bucket, s.Attachments.Quota = b, quota
	s.Notes.Store.Bucket = b
}
