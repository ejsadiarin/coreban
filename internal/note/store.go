package note

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/ejsadiarin/coreban/internal/db/sqlc"
)

// Store persists notes through the sqlc queries in
// internal/db/queries/notes.sql, which scope every statement by owner_id.
type Store struct {
	Pool *pgxpool.Pool
}

func (s *Store) queries() *dbgen.Queries { return dbgen.New(s.Pool) }

// Post adds a note to the end of the owner's feed. text must already be
// clean (CleanText).
func (s *Store) Post(ctx context.Context, owner uuid.UUID, text string) (Note, error) {
	row, err := s.queries().CreateNote(ctx, dbgen.CreateNoteParams{ID: uuid.New(), OwnerID: owner, Body: text})
	if err != nil {
		return Note{}, err
	}
	return fromRow(row), nil
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
	p := Page{Notes: make([]Note, 0, min(len(rows), size))}
	for i, r := range rows {
		if i == size {
			older := Cursor(rows[size-1].Seq).String()
			p.Older = &older
			break
		}
		p.Notes = append(p.Notes, fromRow(r))
	}
	slices.Reverse(p.Notes)
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
// once, but an archived note's text can't change (ErrArchived).
func (s *Store) Edit(ctx context.Context, owner, id uuid.UUID, e Edit) (Note, error) {
	var n Note
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := q.GetNoteForUpdate(ctx, dbgen.GetNoteForUpdateParams{ID: id, OwnerID: owner})
		if err != nil {
			return notFound(err)
		}
		archivedAt := row.ArchivedAt
		if e.Archived != nil {
			switch {
			case *e.Archived && !archivedAt.Valid:
				archivedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			case !*e.Archived:
				archivedAt = pgtype.Timestamptz{}
			}
		}
		body := row.Body
		if e.Text != nil {
			if archivedAt.Valid {
				return ErrArchived
			}
			body = *e.Text
		}
		row, err = q.UpdateNote(ctx, dbgen.UpdateNoteParams{ID: id, OwnerID: owner, Body: body, ArchivedAt: archivedAt})
		if err != nil {
			return err
		}
		n = fromRow(row)
		return nil
	})
	return n, err
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

func fromRow(r dbgen.Note) Note {
	n := Note{ID: r.ID, Text: r.Body, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
	if r.ArchivedAt.Valid {
		at := r.ArchivedAt.Time
		n.ArchivedAt = &at
	}
	return n
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
