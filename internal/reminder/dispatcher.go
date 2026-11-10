package reminder

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/ejsadiarin/coreban/internal/db/sqlc"
)

// Notification is one occurrence of a reminder on its way to the owner.
type Notification struct {
	// Topic is the owner's notification target.
	Topic   string
	Message string
	// At is the occurrence's moment, in the owner's timezone.
	At time.Time
	// Late says it is being sent well after At, such as after downtime.
	Late bool
}

// Notifier delivers a notification, or says why it couldn't.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// lateAfter is how far past its moment an occurrence may go out and still
// count as on time.
const lateAfter = time.Minute

// Run sleeps between dispatches: until the next occurrence's moment, a
// wake, or at most maxIdle (a backstop for changes it was not woken for) —
// never polling the database while there's nothing to send. After a failed
// dispatch or lookup it retries in retryAfter; it never spins faster than
// minWait.
const (
	maxIdle    = time.Hour
	retryAfter = 30 * time.Second
	minWait    = time.Second
)

// Dispatcher sends reminders through a Notifier as their moments come.
// Each occurrence is claimed (advanced past) in its own committed
// transaction before it is sent, so it goes out at most once even if the
// process dies mid-send or several dispatchers run (ADR-0002).
type Dispatcher struct {
	pool     *pgxpool.Pool
	notifier Notifier
	now      func() time.Time
	wake     chan struct{}
}

// NewDispatcher returns a dispatcher reading the time from now.
func NewDispatcher(pool *pgxpool.Pool, n Notifier, now func() time.Time) *Dispatcher {
	return &Dispatcher{pool: pool, notifier: n, now: now, wake: make(chan struct{}, 1)}
}

// Wake makes Run dispatch now rather than at its next planned moment.
func (d *Dispatcher) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Run dispatches until ctx ends: once at start, which catches up anything
// missed while coreban was down, and then whenever a moment comes.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		wait := retryAfter
		switch err := d.Tick(ctx); {
		case err == nil:
			wait = d.untilNext(ctx)
		case errors.Is(err, ErrRetryLater):
			slog.Warn("reminders: ntfy can't take them yet; retrying", "in", retryAfter, "error", err)
		case ctx.Err() == nil:
			slog.Error("reminders: dispatch failed", "error", err)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-d.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// untilNext is how long Run may sleep before the next occurrence's moment.
func (d *Dispatcher) untilNext(ctx context.Context) time.Duration {
	next, err := dbgen.New(d.pool).EarliestFireAt(ctx)
	switch {
	case err != nil:
		if ctx.Err() == nil {
			slog.Error("reminders: find the next moment", "error", err)
		}
		return retryAfter
	case !next.Valid:
		return maxIdle
	}
	return min(max(next.Time.Sub(d.now()), minWait), maxIdle)
}

// Tick sends every occurrence whose moment has come by the dispatcher's
// now, one at a time. It stops at a send that ErrRetryLater says can't get
// through yet, having put that occurrence back, and returns that error.
func (d *Dispatcher) Tick(ctx context.Context) error {
	for {
		now := d.now()
		c, ok, err := d.claim(ctx, now)
		if err != nil || !ok {
			return err
		}
		if !c.send {
			continue
		}
		if err := d.deliver(ctx, c, now); err != nil {
			return err
		}
	}
}

// claimed is one occurrence this dispatcher alone now owns. send is false
// when claiming only moved a recurring reminder on (past missed
// occurrences, or into the owner's new timezone) with nothing to send.
type claimed struct {
	id      uuid.UUID
	message string
	topic   string
	at      time.Time
	once    bool
	send    bool
}

// claim takes the earliest reminder due for attention and advances it past
// the occurrence being sent, committing before anything is sent. A one-off
// has nothing after it. A recurring reminder skips occurrences missed by
// more than lateAfter and moves to the next one; that is the one sent if
// its moment has come.
func (d *Dispatcher) claim(ctx context.Context, now time.Time) (claimed, bool, error) {
	var c claimed
	found := false
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := q.ClaimOccurrence(ctx, timestamptz(now))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim occurrence: %w", err)
		}
		loc, err := time.LoadLocation(row.Timezone)
		if err != nil {
			loc = time.UTC
		}
		c = claimed{id: row.ID, message: row.Message, topic: row.NtfyTopic.String, at: row.NextFireAt.Time.In(loc)}
		found = true
		advance := dbgen.AdvanceReminderParams{ID: row.ID}
		if !row.Rrule.Valid {
			c.once, c.send = true, true
		} else {
			r, err := parseRule(row.Rrule.String, row.LocalStart.Time)
			if err != nil {
				return fmt.Errorf("reminder %s: %w", row.ID, err)
			}
			// From the stored occurrence, unless it was missed or worked out
			// in a zone the owner has left; never at or before the last
			// send, so moving zones can't repeat it.
			from := now.Add(-lateAfter)
			if row.NextTz.String == row.Timezone && row.NextFireAt.Time.After(from) {
				from = row.NextFireAt.Time
			}
			if row.LastDeliveryAt.Valid && !from.After(row.LastDeliveryAt.Time) {
				from = row.LastDeliveryAt.Time.Add(time.Second)
			}
			c.at = r.next(from, loc)
			next := c.at
			if c.send = !c.at.After(now); c.send {
				next = r.next(c.at.Add(time.Second), loc)
			}
			advance.NextFireAt = timestamptz(next)
			advance.NextTz = pgtype.Text{String: row.Timezone, Valid: true}
		}
		if err := q.AdvanceReminder(ctx, advance); err != nil {
			return fmt.Errorf("advance reminder: %w", err)
		}
		return nil
	})
	return c, found, err
}

// deliver sends a claimed occurrence and records how it went. A send that
// may get through later is put back (and its error returned); any other
// failure is recorded, not retried.
func (d *Dispatcher) deliver(ctx context.Context, c claimed, now time.Time) error {
	late := now.Sub(c.at) > lateAfter
	rec := dbgen.RecordDeliveryParams{ID: c.id, At: timestamptz(now), Late: late}
	err := d.notifier.Notify(ctx, Notification{Topic: c.topic, Message: c.message, At: c.at, Late: late})
	if errors.Is(err, ErrRetryLater) {
		// A one-off was advanced to nothing (NULL); put it back unless the
		// owner has rescheduled it since. A recurring occurrence is let go,
		// like any other it misses.
		if c.once {
			if rerr := dbgen.New(d.pool).RestoreReminder(ctx, dbgen.RestoreReminderParams{
				ID: c.id, Occurrence: timestamptz(c.at),
			}); rerr != nil {
				return fmt.Errorf("put back %s: %w", c.id, rerr)
			}
		}
		return err
	}
	if err != nil {
		slog.Warn("reminders: send failed", "reminder", c.id, "error", err)
		rec.Error = pgtype.Text{String: err.Error(), Valid: true}
	} else {
		slog.Info("reminders: sent", "reminder", c.id, "late", late)
	}
	if err := dbgen.New(d.pool).RecordDelivery(ctx, rec); err != nil {
		return fmt.Errorf("record delivery of %s: %w", c.id, err)
	}
	return nil
}
