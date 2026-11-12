// Package feedlink links a record captured from the Feed to its note, in
// the same transaction that creates the record (ADR-0003). Record
// packages (reminders, cards) call it; the note package reads the links.
package feedlink

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/ejsadiarin/coreban/internal/db/sqlc"
)

// Kinds of record a note may link. Expenses and incomes live in
// corefinance, so their notes are made by the note package (on the web's
// word that it created the record), never by Link.
const (
	KindReminder = "reminder"
	KindCard     = "card"
	KindExpense  = "expense"
	KindIncome   = "income"
)

// CrossService reports whether kind's records live in another service.
func CrossService(kind string) bool { return kind == KindExpense || kind == KindIncome }

// ErrNotLinkable is returned for a note that can't take a record: it
// isn't one of the owner's live plain notes.
var ErrNotLinkable = errors.New("note must be one of your plain notes, not archived")

// errBadTarget is a "note" value that is neither "new" nor a note id.
var errBadTarget = errors.New(`note must be "new" or a note's id`)

// Target is the note a record created from the Feed links to: a new one
// at the end of the feed, or (Convert) a plain note linked in place.
type Target struct {
	Convert bool
	NoteID  uuid.UUID
}

// Parse reads a request's "note" value: absent (nil, no note), "new", or
// the id of a plain note to convert.
func Parse(raw *string) (*Target, error) {
	if raw == nil {
		return nil, nil
	}
	if *raw == "new" {
		return &Target{}, nil
	}
	id, err := uuid.Parse(*raw)
	if err != nil {
		return nil, errBadTarget
	}
	return &Target{Convert: true, NoteID: id}, nil
}

// Record names the record being linked: one of KindReminder, KindCard.
type Record struct {
	Kind string
	ID   uuid.UUID
}

// Link gives rec its note as t says, on q (the record's transaction).
func Link(ctx context.Context, q *dbgen.Queries, owner uuid.UUID, t Target, rec Record) error {
	kind := pgtype.Text{String: rec.Kind, Valid: true}
	id := pgtype.UUID{Bytes: rec.ID, Valid: true}
	var reminderID, cardID pgtype.UUID
	switch rec.Kind {
	case KindReminder:
		reminderID = id
	case KindCard:
		cardID = id
	default:
		return errors.New("feedlink: unknown record kind " + rec.Kind)
	}
	if !t.Convert {
		return q.CreateLinkedNote(ctx, dbgen.CreateLinkedNoteParams{
			ID: uuid.New(), OwnerID: owner, LinkKind: kind, ReminderID: reminderID, CardID: cardID,
		})
	}
	n, err := q.LinkNote(ctx, dbgen.LinkNoteParams{
		ID: t.NoteID, OwnerID: owner, LinkKind: kind, ReminderID: reminderID, CardID: cardID,
	})
	if err == nil && n == 0 {
		err = ErrNotLinkable
	}
	return err
}
