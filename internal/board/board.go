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

// statusLabels name each status as the owner reads it.
var statusLabels = map[Status]string{
	StatusTodo:       "Todo",
	StatusInProgress: "In progress",
	StatusDone:       "Done",
}

// Valid reports whether s is one of the fixed statuses.
func (s Status) Valid() bool {
	_, ok := statusLabels[s]
	return ok
}

// Column is an ordered stage on one board.
type Column struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Status   Status    `json:"status"`
	Position int32     `json:"position"`
	// Cards are in priority order. Present on a single board's columns.
	Cards []Card `json:"cards"`
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

// Card is a single piece of work on a board, sitting in exactly one column.
// Its Status is its column's, never stored on the card (ADR-0001).
type Card struct {
	ID          uuid.UUID `json:"id"`
	BoardID     uuid.UUID `json:"board_id"`
	ColumnID    uuid.UUID `json:"column_id"`
	Status      Status    `json:"status"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// defaultColumns are the columns a new board starts with, one per status.
var defaultColumns = []Column{
	{Name: "Todo", Status: StatusTodo},
	{Name: "Doing", Status: StatusInProgress},
	{Name: "Done", Status: StatusDone},
}
