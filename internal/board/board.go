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

// Label is a named tag defined on one board and applied to its cards.
type Label struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
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
	// Labels are the board's labels applied to the card, by name.
	Labels []Label `json:"labels"`
	// Due is when the card is expected to be finished, if ever.
	Due *Due `json:"due"`
	// ArchivedAt is set while the card is archived: hidden from its board,
	// kept, and still counted by its column.
	ArchivedAt *time.Time `json:"archived_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Due is a calendar date ("2026-11-14") or an instant, exactly one of
// them, so it reads back as whichever the owner set.
type Due struct {
	Date string     `json:"date,omitempty"`
	At   *time.Time `json:"at,omitempty"`
}

// dateLayout is how a date-only due is written.
const dateLayout = "2006-01-02"

// inZone renders the card's due instant in loc, the owner's timezone.
func (c Card) inZone(loc *time.Location) Card {
	if c.Due != nil && c.Due.At != nil {
		at := c.Due.At.In(loc)
		c.Due = &Due{At: &at}
	}
	return c
}

// inZone renders the board's cards as Card.inZone does.
func (b Board) inZone(loc *time.Location) Board {
	cols := make([]Column, len(b.Columns))
	for i, col := range b.Columns {
		cols[i] = col.inZone(loc)
	}
	b.Columns = cols
	return b
}

func (c Column) inZone(loc *time.Location) Column {
	if c.Cards == nil {
		return c
	}
	cards := make([]Card, len(c.Cards))
	for i, k := range c.Cards {
		cards[i] = k.inZone(loc)
	}
	c.Cards = cards
	return c
}

// defaultColumns are the columns a new board starts with, one per status.
var defaultColumns = []Column{
	{Name: "Todo", Status: StatusTodo},
	{Name: "Doing", Status: StatusInProgress},
	{Name: "Done", Status: StatusDone},
}
