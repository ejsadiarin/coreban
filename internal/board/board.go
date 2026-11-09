// Package board serves an owner's boards and their columns.
package board

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a board does not exist for the owner. Callers
// must not distinguish "missing" from "belongs to someone else".
var ErrNotFound = errors.New("board not found")

// Status is where a card stands in its lifecycle (ADR-0001): one of a fixed
// set shared by every board. Each column belongs to exactly one.
type Status string

const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
)

// Column is an ordered stage on one board.
type Column struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Status   Status    `json:"status"`
	Position int32     `json:"position"`
}

// Board is an owner's named collection of columns, private to that owner.
type Board struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Columns are in board order. Omitted from list responses.
	Columns []Column `json:"columns,omitempty"`
}

// defaultColumns are the columns a new board starts with, one per status.
var defaultColumns = []Column{
	{Name: "Todo", Status: StatusTodo},
	{Name: "Doing", Status: StatusInProgress},
	{Name: "Done", Status: StatusDone},
}
