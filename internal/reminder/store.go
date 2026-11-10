package reminder

import (
	"context"
	"errors"
	"fmt"
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

// view is how an owner sees their reminders: times in their zone, and
// anything still to be sent paused while they have no notification target.
type view struct {
	loc      *time.Location
	noTarget bool
}

// fromRow renders a row as v shows it.
func fromRow(r dbgen.GetReminderRow, v view) Reminder {
	loc := v.loc
	out := Reminder{
		ID:        r.ID,
		Message:   r.Message,
		Schedule:  Schedule{Kind: KindOnce, At: r.StartsAt.Time.In(loc)},
		CreatedAt: r.CreatedAt.Time.In(loc),
		UpdatedAt: r.UpdatedAt.Time.In(loc),
	}
	if r.NextFireAt.Valid {
		next := r.NextFireAt.Time.In(loc)
		out.NextFireAt = &next
		if v.noTarget {
			reason := PausedNoTarget
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
	return out
}

// Create adds a reminder unless the owner already has limit of them.
func (s *Store) Create(ctx context.Context, owner uuid.UUID, message string, sched Schedule, limit int) (uuid.UUID, error) {
	id := uuid.New()
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
			ID: id, OwnerID: owner, Message: message, StartsAt: timestamptz(sched.At),
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
	return fromRow(row, v), nil
}

// List returns the owner's reminders as v shows them, soonest first.
func (s *Store) List(ctx context.Context, owner uuid.UUID, v view) ([]Reminder, error) {
	rows, err := dbgen.New(s.Pool).ListOwnerReminders(ctx, owner)
	if err != nil {
		return nil, err
	}
	out := make([]Reminder, len(rows))
	for i, r := range rows {
		out[i] = fromRow(dbgen.GetReminderRow(r), v)
	}
	return out, nil
}

// Edit is a partial change: nil fields are left as they are. A new
// schedule re-arms the reminder.
type Edit struct {
	Message  *string
	Schedule *Schedule
}

// Edit applies e to one of the owner's reminders.
func (s *Store) Edit(ctx context.Context, owner, id uuid.UUID, e Edit) error {
	p := dbgen.UpdateReminderParams{ID: id, OwnerID: owner}
	if e.Message != nil {
		p.Message = pgtype.Text{String: *e.Message, Valid: true}
	}
	if e.Schedule != nil {
		p.StartsAt = timestamptz(e.Schedule.At)
	}
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
