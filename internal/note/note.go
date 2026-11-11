// Package note serves an owner's feed: the notes they post to themselves,
// newest last, like a chat with themselves. This slice has plain notes
// only; linked records (ADR-0003) come later.
package note

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	// ErrNotFound is returned when a note does not exist for the owner.
	// Callers must not distinguish "missing" from "belongs to someone else".
	ErrNotFound = errors.New("note not found")
	// ErrArchived is returned when editing the text of an archived note.
	ErrArchived = errors.New("note is archived")
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
	ID   uuid.UUID `json:"id"`
	Text string    `json:"text"`
	// ArchivedAt is set while the note is archived: collapsed in the feed,
	// kept, and still in its place.
	ArchivedAt *time.Time `json:"archived_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
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
