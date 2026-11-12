// Package note serves an owner's feed: the notes they post to themselves,
// newest last, like a chat with themselves. A note is plain text, or a
// window onto one linked record, shown as it is now (ADR-0003): a
// reminder or card here, or an expense or income in corefinance.
package note

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/reminder"
)

var (
	// ErrNotFound is returned when a note does not exist for the owner.
	// Callers must not distinguish "missing" from "belongs to someone else".
	ErrNotFound = errors.New("note not found")
	// ErrArchived is returned when editing the text of an archived note.
	ErrArchived = errors.New("note is archived")
	// ErrLinked is returned when editing the text of a linked note, which
	// has none of its own: its record is edited instead.
	ErrLinked = errors.New("note shows a linked record")
	// ErrRecordTaken is returned when linking a record that already has a
	// note: a record has at most one.
	ErrRecordTaken = errors.New("record already has a note")
	// ErrTakesNoSnapshot is returned when the web offers a snapshot for a
	// note whose snapshot coreban keeps itself (a reminder or card), or a
	// plain note, which has none.
	ErrTakesNoSnapshot = errors.New("note takes no snapshot from the web")
)

// MaxTextLength is the most characters a note's text may hold.
const MaxTextLength = 4000

// Feed paging: how many notes a page holds unless asked for fewer, and
// the most it ever holds.
const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// Note is an item in the owner's feed.
type Note struct {
	ID uuid.UUID `json:"id"`
	// Text is a plain note's text; empty on a linked note.
	Text string `json:"text"`
	// Link is the record a linked note shows; nil on a plain note.
	Link *Link `json:"link"`
	// ArchivedAt is set while the note is archived: collapsed in the feed,
	// kept, and still in its place.
	ArchivedAt *time.Time `json:"archived_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Link is the one record a note shows: live while it exists, and its last
// state (a snapshot) once it was deleted.
type Link struct {
	// Kind is "reminder", "card", "expense", or "income".
	Kind    string `json:"kind"`
	Deleted bool   `json:"deleted"`
	// RecordID names an expense or income in corefinance, which the web
	// reads live itself; coreban only knows it is gone once the web saved
	// a snapshot (Deleted).
	RecordID *uuid.UUID `json:"record_id,omitempty"`
	// At most one of these is set, for Kind: a reminder or card live or
	// from its snapshot, an expense or income only from its snapshot.
	// None if a deleted record left no snapshot.
	Reminder *reminder.Reminder `json:"reminder,omitempty"`
	Card     *Card              `json:"card,omitempty"`
	Expense  json.RawMessage    `json:"expense,omitempty"`
	Income   json.RawMessage    `json:"income,omitempty"`
}

// Card is a linked card as its note shows it: its title, the board and
// column it is in, and its status.
type Card struct {
	ID         uuid.UUID  `json:"id"`
	BoardID    uuid.UUID  `json:"board_id"`
	BoardName  string     `json:"board_name"`
	ColumnName string     `json:"column_name"`
	Status     string     `json:"status"`
	Title      string     `json:"title"`
	ArchivedAt *time.Time `json:"archived_at"`
}

// Page is one screen of the feed, oldest first. Older is the cursor for
// the page before it, absent when this page reaches the first note.
type Page struct {
	Notes []Note  `json:"notes"`
	Older *string `json:"older"`
}

// Cursor is a position in the feed; a page asked for "before" it holds
// only notes posted earlier.
type Cursor int64

func (c Cursor) String() string { return strconv.FormatInt(int64(c), 10) }

// ParseCursor reads a cursor from a page's Older value.
func ParseCursor(s string) (Cursor, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, errors.New("invalid cursor")
	}
	return Cursor(n), nil
}

// CleanText trims a note's text and reports why it can't be posted, if
// it can't: it is blank, or longer than MaxTextLength characters.
func CleanText(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", errors.New("text is required")
	case utf8.RuneCountInString(s) > MaxTextLength:
		return "", errors.New("text is longer than " + strconv.Itoa(MaxTextLength) + " characters")
	}
	return s, nil
}
