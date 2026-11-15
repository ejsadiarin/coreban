package settings

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ejsadiarin/coreban/internal/auth"
	dbgen "github.com/ejsadiarin/coreban/internal/db/sqlc"
	"github.com/ejsadiarin/coreban/internal/moment"
)

// ErrTopicTaken is returned when another owner already uses the topic.
var ErrTopicTaken = errors.New("ntfy topic already in use")

// uniqueViolation is Postgres's SQLSTATE for a unique constraint breach.
const uniqueViolation = "23505"

// Store persists settings through internal/db/queries/settings.sql, keyed
// by owner.
type Store struct {
	Pool *pgxpool.Pool
}

// Get returns the owner's settings, or the defaults if they have saved none.
func (s *Store) Get(ctx context.Context, owner uuid.UUID) (Settings, error) {
	topic, err := dbgen.New(s.Pool).GetOwnerSettings(ctx, owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	out := Settings{Saved: true}
	if topic.Valid {
		out.NtfyTopic = &topic.String
	}
	return out, nil
}

// Put replaces the owner's settings. They must already be validated. zone
// is the owner's zone (Zone), stored as the one last seen if this is their
// first save.
func (s *Store) Put(ctx context.Context, owner uuid.UUID, v Settings, zone *time.Location) error {
	var topic pgtype.Text
	if v.NtfyTopic != nil {
		topic = pgtype.Text{String: *v.NtfyTopic, Valid: true}
	}
	err := dbgen.New(s.Pool).PutOwnerSettings(ctx, dbgen.PutOwnerSettingsParams{
		OwnerID: owner, NtfyTopic: topic, Timezone: zone.String(),
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return ErrTopicTaken
	}
	return err
}

// Zone is the owner's timezone, which their times are read and shown in:
// the request token's tz claim when it is about owner, else the zone
// coreban last saw for them (UTC if none). A claim no zone database here
// knows is an error, never a guess.
func (s *Store) Zone(ctx context.Context, owner uuid.UUID) (*time.Location, error) {
	if tz, about, ok := auth.TimezoneFromContext(ctx); ok && about == owner {
		return moment.LoadZone(tz)
	}
	tz, err := dbgen.New(s.Pool).GetOwnerTimezone(ctx, owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.UTC, nil
	}
	if err != nil {
		return nil, err
	}
	return moment.LoadZone(tz)
}

// SeeZone records tz, from a token's claim, as the zone last seen for
// owner, reporting whether it changed. Only an owner with settings has a
// row to record it on; their first save stores it (Put). An unloadable tz
// is never recorded.
func (s *Store) SeeZone(ctx context.Context, owner uuid.UUID, tz string) (bool, error) {
	if _, err := moment.LoadZone(tz); err != nil {
		return false, nil
	}
	q := dbgen.New(s.Pool)
	last, err := q.GetOwnerTimezone(ctx, owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && last == tz) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	n, err := q.RecordOwnerTimezone(ctx, dbgen.RecordOwnerTimezoneParams{OwnerID: owner, Timezone: tz})
	return n > 0, err
}
