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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/ejsadiarin/coreban/internal/attachment"
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
	// Bucket holds attachment files; nil when none is configured.
	Bucket *attachment.Bucket
	// FilesQueued hears that a deleted note queued its attachments' files
	// for deletion (the attachment cleaner's Wake); nil for none.
	FilesQueued func()
}

func (s *Store) queries() *dbgen.Queries { return dbgen.New(s.Pool) }

// Post adds a note to the end of the owner's feed: text (already clean,
// CleanText; empty for none), uploads (attachment ids, distinct, at most
// attachment.MaxPerNote, in album order), or both. Each upload must be
// the owner's, not yet posted (ErrNotYourUpload), and in the bucket at the
// size it declared (ErrNotUploaded).
func (s *Store) Post(ctx context.Context, owner uuid.UUID, text string, uploads []uuid.UUID) (Note, error) {
	if text == "" && len(uploads) == 0 {
		return Note{}, ErrTextRequired
	}
	if len(uploads) > 0 {
		if err := s.checkUploads(ctx, owner, uploads); err != nil {
			return Note{}, err
		}
	}
	var row dbgen.Note
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		var err error
		row, err = q.CreateNote(ctx, dbgen.CreateNoteParams{
			ID: uuid.New(), OwnerID: owner, Body: pgtype.Text{String: text, Valid: text != ""},
			AttachmentCount: int16(len(uploads)),
		})
		if err != nil || len(uploads) == 0 {
			return err
		}
		// Another post may have taken an upload since checkUploads.
		n, err := q.AttachToNote(ctx, dbgen.AttachToNoteParams{
			NoteID: pgtype.UUID{Bytes: row.ID, Valid: true}, OwnerID: owner, Ids: uploads,
		})
		if err == nil && n != int64(len(uploads)) {
			err = ErrNotYourUpload
		}
		return err
	})
	if err != nil {
		return Note{}, err
	}
	return s.one(ctx, owner, row)
}

// checkUploads makes sure every upload is the owner's, waiting, and whole
// in the bucket. It runs before the note's transaction: it calls the
// bucket, which shouldn't hold a transaction open.
func (s *Store) checkUploads(ctx context.Context, owner uuid.UUID, uploads []uuid.UUID) error {
	if s.Bucket == nil {
		return attachment.ErrNoBucket
	}
	rows, err := s.queries().ListUploads(ctx, dbgen.ListUploadsParams{OwnerID: owner, Ids: uploads})
	if err != nil {
		return err
	}
	if len(rows) != len(uploads) {
		return ErrNotYourUpload
	}
	// One round trip for all of them, not one per file.
	g, gctx := errgroup.WithContext(ctx)
	for _, a := range rows {
		g.Go(func() error {
			ok, err := s.Bucket.Uploaded(gctx, a)
			if err != nil {
				return fmt.Errorf("check upload %s: %w", a.ID, err)
			}
			if !ok {
				return ErrNotUploaded
			}
			return nil
		})
	}
	return g.Wait()
}

// PostRecord adds a note to the end of the owner's feed showing the
// expense or income (feedlink.CrossService) the web just created in
// corefinance. coreban can't check recordID; a record already linked to
// one of the owner's notes is refused (ErrRecordTaken).
func (s *Store) PostRecord(ctx context.Context, owner uuid.UUID, kind string, recordID uuid.UUID) (Note, error) {
	row, err := s.queries().CreateRecordNote(ctx, dbgen.CreateRecordNoteParams{
		ID: uuid.New(), OwnerID: owner,
		LinkKind: pgtype.Text{String: kind, Valid: true}, RecordID: pgtype.UUID{Bytes: recordID, Valid: true},
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "notes_record_key" {
		return Note{}, ErrRecordTaken
	}
	if err != nil {
		return Note{}, err
	}
	return s.one(ctx, owner, row)
}

const uniqueViolation = "23505"

// Feed returns one page of the owner's feed: the newest size notes placed
// before the cursor (or the newest of all, with none), oldest first, within
// scope (ScopeAll, ScopeBudget or ScopeNotes). Archived notes are left out
// unless withArchived (ADR-0006). Links to attachments are
// signed only with links set: an admin's targeted read of another owner
// sees that they exist, never the files.
func (s *Store) Feed(ctx context.Context, owner uuid.UUID, before *Cursor, scope string, withArchived bool, size int, links bool) (Page, error) {
	params := dbgen.ListNotesParams{OwnerID: owner, RowLimit: int32(size + 1), WithArchived: withArchived}
	if before != nil {
		params.BeforePostedAt = pgtype.Timestamptz{Time: before.PostedAt, Valid: true}
		params.BeforeSeq = pgtype.Int8{Int64: before.Seq, Valid: true}
	}
	if scope != ScopeAll {
		params.Scope = pgtype.Text{String: scope, Valid: true}
	}
	rows, err := s.queries().ListNotes(ctx, params)
	if err != nil {
		return Page{}, err
	}
	var p Page
	if len(rows) > size {
		last := rows[size-1]
		older := Cursor{PostedAt: last.PostedAt.Time, Seq: last.Seq}.String()
		p.Older = &older
		rows = rows[:size]
	}
	slices.Reverse(rows)
	if p.Notes, err = s.render(ctx, owner, rows, links); err != nil {
		return Page{}, err
	}
	return p, nil
}

// Sync gives each budget record a note on its own date (ADR-0005), in one
// statement: a record without a note gets one at its PostedAt, and a note
// whose local date (in tz, an IANA name) isn't its record's moves to
// PostedAt, which must fall on the record's date. A record named twice
// counts once. It reports how many notes it created and moved.
func (s *Store) Sync(ctx context.Context, owner uuid.UUID, tz string, records []SyncRecord) (created, moved int, err error) {
	p := dbgen.SyncRecordNotesParams{OwnerID: owner, Tz: tz}
	seen := map[[2]string]bool{}
	for _, r := range records {
		k := [2]string{r.Kind, r.ID.String()}
		if seen[k] {
			continue
		}
		seen[k] = true
		p.Ids = append(p.Ids, uuid.New())
		p.Kinds = append(p.Kinds, r.Kind)
		p.RecordIds = append(p.RecordIds, r.ID)
		p.PostedAts = append(p.PostedAts, pgtype.Timestamptz{Time: r.PostedAt, Valid: true})
	}
	if len(p.Ids) == 0 {
		return 0, 0, nil
	}
	rows, err := s.queries().SyncRecordNotes(ctx, p)
	if err != nil {
		return 0, 0, fmt.Errorf("sync budget notes: %w", err)
	}
	for _, inserted := range rows {
		if inserted {
			created++
		} else {
			moved++
		}
	}
	return created, moved, nil
}

// DeleteRecord deletes the note of one of the owner's budget records when
// the sync made it. A note posted from the Feed stays (ErrPostedFromFeed);
// a record with no note is ErrNotFound.
func (s *Store) DeleteRecord(ctx context.Context, owner uuid.UUID, kind string, recordID uuid.UUID) error {
	origin, err := s.queries().DeleteRecordNote(ctx, dbgen.DeleteRecordNoteParams{
		OwnerID: owner, LinkKind: pgtype.Text{String: kind, Valid: true},
		RecordID: pgtype.UUID{Bytes: recordID, Valid: true},
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return err
	case origin != OriginBudget:
		return ErrPostedFromFeed
	}
	return nil
}

// Edit is a change to one note; nil fields are left as they are.
type Edit struct {
	// Text must already be clean (CleanText); empty clears it, which only
	// a note with attachments may do (ErrTextRequired).
	Text     *string
	Archived *bool
	// Snapshot is what the web last showed of an expense or income it
	// found gone: a JSON object, kept only if the note has none yet.
	Snapshot json.RawMessage
}

// Edit applies e to one of the owner's notes. Archiving or restoring
// happens before a text change, so a note can be restored and edited at
// once, but an archived note's text can't change (ErrArchived), and a
// linked note has none of its own (ErrLinked).
//
// Archiving a linked note archives its card, or deletes its reminder for
// good, in the same transaction. Restoring brings back a card that was
// archived along with the note; a deleted reminder stays deleted.
// Archiving an expense or income note changes only the note: the web has
// deleted the record in corefinance first (ADR-0003), and sends the
// content it last showed as the snapshot.
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
		if e.Snapshot != nil {
			if !cur.RecordID.Valid {
				return ErrTakesNoSnapshot
			}
			if err := q.SaveRecordSnapshot(ctx, dbgen.SaveRecordSnapshotParams{
				ID: id, OwnerID: owner, Snapshot: e.Snapshot,
			}); err != nil {
				return fmt.Errorf("save snapshot: %w", err)
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
			if *e.Text == "" && cur.AttachmentCount == 0 {
				return ErrTextRequired
			}
			body = pgtype.Text{String: *e.Text, Valid: *e.Text != ""}
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

// Delete permanently deletes one of the owner's notes. Its attachments'
// rows go with it, and their files are queued for the cleaner in the same
// transaction (ADR-0004).
func (s *Store) Delete(ctx context.Context, owner, id uuid.UUID) error {
	attached, err := s.queries().DeleteNote(ctx, dbgen.DeleteNoteParams{ID: id, OwnerID: owner})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if attached > 0 && s.FilesQueued != nil {
		s.FilesQueued()
	}
	return nil
}

func (s *Store) one(ctx context.Context, owner uuid.UUID, row dbgen.Note) (Note, error) {
	notes, err := s.render(ctx, owner, []dbgen.Note{row}, true)
	if err != nil {
		return Note{}, err
	}
	return notes[0], nil
}

// render turns rows into notes, reading every linked record live in one
// query per kind, and a deleted one from its note's snapshot. links signs
// attachment links (see Feed).
func (s *Store) render(ctx context.Context, owner uuid.UUID, rows []dbgen.Note, links bool) ([]Note, error) {
	var reminderIDs, cardIDs, attachedIDs []uuid.UUID
	for _, r := range rows {
		if r.AttachmentCount > 0 {
			attachedIDs = append(attachedIDs, r.ID)
		}
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

	attached := map[uuid.UUID][]attachment.View{}
	if len(attachedIDs) > 0 {
		all, err := q.ListNoteAttachments(ctx, dbgen.ListNoteAttachmentsParams{OwnerID: owner, NoteIds: attachedIDs})
		if err != nil {
			return nil, err
		}
		views, err := attachment.Render(ctx, s.Bucket, all, links)
		if err != nil {
			return nil, err
		}
		for i, a := range all {
			attached[a.NoteID.Bytes] = append(attached[a.NoteID.Bytes], views[i])
		}
	}

	out := make([]Note, len(rows))
	for i, r := range rows {
		n := Note{
			ID: r.ID, Text: r.Body.String, Attachments: attached[r.ID],
			PostedAt: r.PostedAt.Time, Origin: r.Origin,
			CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time,
		}
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
			case feedlink.KindExpense, feedlink.KindIncome:
				id := uuid.UUID(r.RecordID.Bytes)
				l.RecordID = &id
				l.Deleted = r.Snapshot != nil
				if l.Kind == feedlink.KindExpense {
					l.Expense = r.Snapshot
				} else {
					l.Income = r.Snapshot
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
