package reminder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/ejsadiarin/coreban/internal/db/sqlc"
)

// Store persists reminders through internal/db/queries/reminders.sql. Every
// method is scoped to one owner.
type Store struct {
	Pool *pgxpool.Pool
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// view is how an owner sees their reminders at a moment: times in their
// zone, and anything still to be sent paused while they have no
// notification target.
type view struct {
	loc      *time.Location
	noTarget bool
	now      time.Time
}

// scheduleRow is a schedule as stored: a one-off's starts_at, or a
// recurring rule with its next occurrence worked out in the owner's zone.
type scheduleRow struct {
	startsAt   pgtype.Timestamptz
	rrule      pgtype.Text
	localStart pgtype.Timestamp
	next       pgtype.Timestamptz
	nextTZ     pgtype.Text
}

// rowOf stores s for an owner seeing it as v.
func rowOf(s Schedule, v view) scheduleRow {
	if s.Kind == KindOnce {
		return scheduleRow{startsAt: timestamptz(*s.At), next: timestamptz(*s.At)}
	}
	r := ruleOf(s, v.now.In(v.loc).Format(dateLayout))
	return scheduleRow{
		rrule:      pgtype.Text{String: r.rrule(), Valid: true},
		localStart: pgtype.Timestamp{Time: r.start, Valid: true},
		next:       timestamptz(r.next(v.now, v.loc)),
		nextTZ:     pgtype.Text{String: v.loc.String(), Valid: true},
	}
}

// fromRow renders a row as v shows it. A recurring reminder's next
// occurrence is worked out afresh if the stored one is behind v's moment
// or in a zone the owner has left.
func fromRow(r dbgen.GetReminderRow, v view) (Reminder, error) {
	loc := v.loc
	out := Reminder{
		ID:        r.ID,
		Message:   r.Message,
		CreatedAt: r.CreatedAt.Time.In(loc),
		UpdatedAt: r.UpdatedAt.Time.In(loc),
	}
	var next *time.Time
	if r.NextFireAt.Valid {
		n := r.NextFireAt.Time
		next = &n
	}
	if r.Rrule.Valid {
		rl, err := parseRule(r.Rrule.String, r.LocalStart.Time)
		if err != nil {
			return Reminder{}, err
		}
		out.Schedule = rl.schedule()
		if next == nil || next.Before(v.now) || r.NextTz.String != loc.String() {
			n := rl.next(v.now, loc)
			next = &n
		}
	} else {
		at := r.StartsAt.Time.In(loc)
		out.Schedule = Schedule{Kind: KindOnce, At: &at}
	}
	if r.CardID.Valid {
		out.Card = &CardRef{ID: r.CardID.Bytes, BoardID: r.CardBoardID.Bytes, Title: r.CardTitle.String}
	}
	if next != nil {
		n := next.In(loc)
		out.NextFireAt = &n
		reason := ""
		switch {
		case v.noTarget:
			reason = PausedNoTarget
		case r.CardArchived:
			reason = PausedCardArchived
		case r.CardCompleted:
			reason = PausedCardCompleted
		}
		if reason != "" {
			out.Paused = &reason
		}
	}
	if r.LastDeliveryAt.Valid {
		d := Delivery{At: r.LastDeliveryAt.Time.In(loc), Late: r.LastDeliveryLate}
		if r.LastDeliveryError.Valid {
			d.Error = &r.LastDeliveryError.String
		}
		out.LastDelivery = &d
	}
	return out, nil
}

func nullUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

// card is one of the owner's cards as reminders need it.
type card struct {
	ref CardRef
	due *time.Time // its due as an instant; nil without one
	// dueDay is set for a due that is a calendar day, not an instant.
	dueDay *time.Time
}

// ownedCard looks up one of the owner's cards; any other id is invalid.
func (s *Store) ownedCard(ctx context.Context, owner, id uuid.UUID) (card, error) {
	row, err := dbgen.New(s.Pool).GetOwnedCard(ctx, dbgen.GetOwnedCardParams{ID: id, OwnerID: owner})
	if errors.Is(err, pgx.ErrNoRows) {
		return card{}, errNotYourCard
	}
	if err != nil {
		return card{}, err
	}
	c := card{ref: CardRef{ID: row.ID, BoardID: row.BoardID, Title: row.Title}}
	if row.DueAt.Valid {
		c.due = &row.DueAt.Time
	}
	if row.DueDate.Valid {
		c.dueDay = &row.DueDate.Time
	}
	return c, nil
}

// Create adds a reminder, attached to cardID if set, unless the owner
// already has limit of them.
func (s *Store) Create(ctx context.Context, owner uuid.UUID, message string, sched Schedule, cardID *uuid.UUID, v view, limit int) (uuid.UUID, error) {
	id := uuid.New()
	row := rowOf(sched, v)
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		if err := q.LockOwnerReminders(ctx, owner); err != nil {
			return err
		}
		n, err := q.CountOwnerReminders(ctx, owner)
		if err != nil {
			return err
		}
		if n >= int64(limit) {
			return ErrCapReached
		}
		if err := q.CreateReminder(ctx, dbgen.CreateReminderParams{
			ID: id, OwnerID: owner, Message: message, StartsAt: row.startsAt, Rrule: row.rrule,
			LocalStart: row.localStart, NextFireAt: row.next, NextTz: row.nextTZ, CardID: nullUUID(cardID),
		}); err != nil {
			return fmt.Errorf("insert reminder: %w", err)
		}
		return nil
	})
	return id, err
}

// Get returns one of the owner's reminders as v shows it.
func (s *Store) Get(ctx context.Context, owner, id uuid.UUID, v view) (Reminder, error) {
	row, err := dbgen.New(s.Pool).GetReminder(ctx, dbgen.GetReminderParams{ID: id, OwnerID: owner})
	if errors.Is(err, pgx.ErrNoRows) {
		return Reminder{}, ErrNotFound
	}
	if err != nil {
		return Reminder{}, err
	}
	return fromRow(row, v)
}

// List returns the owner's reminders (only those on cardID, if set) as v
// shows them: soonest first, then those with nothing left to send.
func (s *Store) List(ctx context.Context, owner uuid.UUID, cardID *uuid.UUID, v view) ([]Reminder, error) {
	rows, err := dbgen.New(s.Pool).ListOwnerReminders(ctx, dbgen.ListOwnerRemindersParams{OwnerID: owner, CardID: nullUUID(cardID)})
	if err != nil {
		return nil, err
	}
	out := make([]Reminder, len(rows))
	for i, r := range rows {
		if out[i], err = fromRow(dbgen.GetReminderRow(r), v); err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(out, func(a, b Reminder) int {
		switch {
		case a.NextFireAt == nil && b.NextFireAt == nil:
			return 0
		case a.NextFireAt == nil:
			return 1
		case b.NextFireAt == nil:
			return -1
		}
		return a.NextFireAt.Compare(*b.NextFireAt)
	})
	return out, nil
}

// Edit is a partial change: nil fields are left as they are. A new
// schedule replaces the old one and re-arms the reminder.
type Edit struct {
	Message  *string
	Schedule *Schedule
	// SetCard attaches the reminder to Card, or detaches it if Card is nil.
	SetCard bool
	Card    *uuid.UUID
}

// Edit applies e to one of the owner's reminders, seen as v.
func (s *Store) Edit(ctx context.Context, owner, id uuid.UUID, e Edit, v view) error {
	p := dbgen.UpdateReminderParams{ID: id, OwnerID: owner}
	if e.Message != nil {
		p.Message = pgtype.Text{String: *e.Message, Valid: true}
	}
	if e.Schedule != nil {
		row := rowOf(*e.Schedule, v)
		p.SetSchedule = true
		p.StartsAt, p.Rrule, p.LocalStart, p.NextFireAt, p.NextTz = row.startsAt, row.rrule, row.localStart, row.next, row.nextTZ
	}
	p.SetCard, p.CardID = e.SetCard, nullUUID(e.Card)
	return found(dbgen.New(s.Pool).UpdateReminder(ctx, p))
}

// Delete removes one of the owner's reminders for good.
func (s *Store) Delete(ctx context.Context, owner, id uuid.UUID) error {
	return found(dbgen.New(s.Pool).DeleteReminder(ctx, dbgen.DeleteReminderParams{ID: id, OwnerID: owner}))
}

// found turns a write that touched no row into ErrNotFound.
func found(rows int64, err error) error {
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}
