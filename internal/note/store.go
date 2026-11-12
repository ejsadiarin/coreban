package note

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/ejsadiarin/coreban/internal/db/sqlc"
	"github.com/ejsadiarin/coreban/internal/feedlink"
	"github.com/ejsadiarin/coreban/internal/reminder"
)

// ReminderRenderer shows reminder rows the way the owner sees their
// reminders now (reminder.Handler.Render).
type ReminderRenderer func(ctx context.Context, owner uuid.UUID, rows []dbgen.GetReminderRow) ([]reminder.Reminder, error)

// Store persists notes through the sqlc queries in
// internal/db/queries/notes.sql, which scope every statement by owner_id.
type Store struct {
	Pool *pgxpool.Pool
	// Reminders renders linked reminders.
	Reminders ReminderRenderer
}

func (s *Store) queries() *dbgen.Queries { return dbgen.New(s.Pool) }

// Post adds a note to the end of the owner's feed. text must already be
// clean (CleanText).
func (s *Store) Post(ctx context.Context, owner uuid.UUID, text string) (Note, error) {
	row, err := s.queries().CreateNote(ctx, dbgen.CreateNoteParams{
		ID: uuid.New(), OwnerID: owner, Body: pgtype.Text{String: text, Valid: true},
	})
	if err != nil {
		return Note{}, err
	}
	return s.one(ctx, owner, row)
}

// Feed returns one page of the owner's feed: the newest size notes posted
// before the cursor (or the newest of all, with none), oldest first.
func (s *Store) Feed(ctx context.Context, owner uuid.UUID, before *Cursor, size int) (Page, error) {
	params := dbgen.ListNotesParams{OwnerID: owner, RowLimit: int32(size + 1)}
	if before != nil {
		params.BeforeSeq = pgtype.Int8{Int64: int64(*before), Valid: true}
	}
	rows, err := s.queries().ListNotes(ctx, params)
	if err != nil {
		return Page{}, err
	}
	var p Page
	if len(rows) > size {
		older := Cursor(rows[size-1].Seq).String()
		p.Older = &older
		rows = rows[:size]
	}
	slices.Reverse(rows)
	if p.Notes, err = s.render(ctx, owner, rows); err != nil {
		return Page{}, err
	}
	return p, nil
}

// Edit is a change to one note; nil fields are left as they are.
type Edit struct {
	// Text must already be clean (CleanText).
	Text     *string
	Archived *bool
}

// Edit applies e to one of the owner's notes. Archiving or restoring
// happens before a text change, so a note can be restored and edited at
// once, but an archived note's text can't change (ErrArchived), and a
// linked note has none of its own (ErrLinked).
//
// Archiving a linked note archives its card, or deletes its reminder for
// good, in the same transaction. Restoring brings back a card that was
// archived along with the note; a deleted reminder stays deleted.
func (s *Store) Edit(ctx context.Context, owner, id uuid.UUID, e Edit) (Note, error) {
	var row dbgen.Note
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		cur, err := q.GetNoteForUpdate(ctx, dbgen.GetNoteForUpdateParams{ID: id, OwnerID: owner})
		if err != nil {
			return notFound(err)
		}
		archivedAt, archivedCard := cur.ArchivedAt, cur.ArchivedCard
		if e.Archived != nil {
			switch {
			case *e.Archived && !archivedAt.Valid:
				archivedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
				if archivedCard, err = archiveRecord(ctx, q, owner, cur); err != nil {
					return err
				}
			case !*e.Archived && archivedAt.Valid:
				archivedAt = pgtype.Timestamptz{}
				if cur.ArchivedCard && cur.CardID.Valid {
					if err := q.RestoreCard(ctx, cur.CardID.Bytes); err != nil {
						return fmt.Errorf("restore card: %w", err)
					}
				}
				archivedCard = false
			}
		}
		body := cur.Body
		if e.Text != nil {
			switch {
			case cur.LinkKind.Valid:
				return ErrLinked
			case archivedAt.Valid:
				return ErrArchived
			}
			body = pgtype.Text{String: *e.Text, Valid: true}
		}
		row, err = q.UpdateNote(ctx, dbgen.UpdateNoteParams{
			ID: id, OwnerID: owner, Body: body, ArchivedAt: archivedAt, ArchivedCard: archivedCard,
		})
		return err
	})
	if err != nil {
		return Note{}, err
	}
	return s.one(ctx, owner, row)
}

// archiveRecord does to a note's record what archiving the note does: its
// card is archived (reporting whether this did it), its reminder deleted.
// The record goes first, so the note is never archived while its record
// lives on.
func archiveRecord(ctx context.Context, q *dbgen.Queries, owner uuid.UUID, n dbgen.Note) (bool, error) {
	switch {
	case n.CardID.Valid:
		rows, err := q.ArchiveLinkedCard(ctx, dbgen.ArchiveLinkedCardParams{ID: n.CardID.Bytes, OwnerID: owner})
		if err != nil {
			return false, fmt.Errorf("archive card: %w", err)
		}
		return rows > 0, nil
	case n.ReminderID.Valid:
		if err := q.DeleteLinkedReminder(ctx, dbgen.DeleteLinkedReminderParams{ID: n.ReminderID.Bytes, OwnerID: owner}); err != nil {
			return false, fmt.Errorf("delete reminder: %w", err)
		}
	}
	return false, nil
}

// Delete permanently removes one of the owner's notes.
func (s *Store) Delete(ctx context.Context, owner, id uuid.UUID) error {
	n, err := s.queries().DeleteNote(ctx, dbgen.DeleteNoteParams{ID: id, OwnerID: owner})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) one(ctx context.Context, owner uuid.UUID, row dbgen.Note) (Note, error) {
	notes, err := s.render(ctx, owner, []dbgen.Note{row})
	if err != nil {
		return Note{}, err
	}
	return notes[0], nil
}

// render turns rows into notes, reading every linked record live in one
// query per kind, and a deleted one from its note's snapshot.
func (s *Store) render(ctx context.Context, owner uuid.UUID, rows []dbgen.Note) ([]Note, error) {
	var reminderIDs, cardIDs []uuid.UUID
	for _, r := range rows {
		if r.ReminderID.Valid {
			reminderIDs = append(reminderIDs, r.ReminderID.Bytes)
		}
		if r.CardID.Valid {
			cardIDs = append(cardIDs, r.CardID.Bytes)
		}
	}
	q := s.queries()

	// Live and snapshot reminders render together: one owner view for all.
	var reminderRows []dbgen.GetReminderRow
	reminderAt := map[int]int{} // note index → reminderRows index
	if len(reminderIDs) > 0 {
		live, err := q.ListNoteReminders(ctx, dbgen.ListNoteRemindersParams{OwnerID: owner, Ids: reminderIDs})
		if err != nil {
			return nil, err
		}
		byID := map[uuid.UUID]dbgen.GetReminderRow{}
		for _, r := range live {
			byID[r.ID] = dbgen.GetReminderRow(r)
		}
		for i, r := range rows {
			if lr, ok := byID[r.ReminderID.Bytes]; ok && r.ReminderID.Valid {
				reminderAt[i] = len(reminderRows)
				reminderRows = append(reminderRows, lr)
			}
		}
	}
	for i, r := range rows {
		if r.LinkKind.String != feedlink.KindReminder || r.ReminderID.Valid || r.Snapshot == nil {
			continue
		}
		var snap dbgen.GetReminderRow
		if err := json.Unmarshal(r.Snapshot, &snap); err != nil {
			return nil, fmt.Errorf("note %s: reminder snapshot: %w", r.ID, err)
		}
		reminderAt[i] = len(reminderRows)
		reminderRows = append(reminderRows, snap)
	}
	var reminders []reminder.Reminder
	if len(reminderRows) > 0 {
		var err error
		if reminders, err = s.Reminders(ctx, owner, reminderRows); err != nil {
			return nil, err
		}
	}

	cards := map[uuid.UUID]dbgen.ListNoteCardsRow{}
	if len(cardIDs) > 0 {
		live, err := q.ListNoteCards(ctx, dbgen.ListNoteCardsParams{OwnerID: owner, Ids: cardIDs})
		if err != nil {
			return nil, err
		}
		for _, c := range live {
			cards[c.ID] = c
		}
	}

	out := make([]Note, len(rows))
	for i, r := range rows {
		n := Note{ID: r.ID, Text: r.Body.String, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
		if r.ArchivedAt.Valid {
			at := r.ArchivedAt.Time
			n.ArchivedAt = &at
		}
		if r.LinkKind.Valid {
			l := &Link{Kind: r.LinkKind.String}
			switch l.Kind {
			case feedlink.KindReminder:
				l.Deleted = !r.ReminderID.Valid
				if j, ok := reminderAt[i]; ok {
					l.Reminder = &reminders[j]
				}
			case feedlink.KindCard:
				l.Deleted = !r.CardID.Valid
				c, ok := cards[r.CardID.Bytes]
				if !r.CardID.Valid && r.Snapshot != nil {
					if err := json.Unmarshal(r.Snapshot, &c); err != nil {
						return nil, fmt.Errorf("note %s: card snapshot: %w", r.ID, err)
					}
					ok = true
				}
				if ok {
					l.Card = cardOf(c)
				}
			}
			n.Link = l
		}
		out[i] = n
	}
	return out, nil
}

func cardOf(c dbgen.ListNoteCardsRow) *Card {
	out := &Card{
		ID: c.ID, BoardID: c.BoardID, BoardName: c.BoardName, ColumnName: c.ColumnName,
		Status: c.Status, Title: c.Title,
	}
	if c.ArchivedAt.Valid {
		at := c.ArchivedAt.Time
		out.ArchivedAt = &at
	}
	return out
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
