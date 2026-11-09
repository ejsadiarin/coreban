package settings

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/ejsadiarin/coreban/internal/db/sqlc"
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
	row, err := dbgen.New(s.Pool).GetOwnerSettings(ctx, owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaults, nil
	}
	if err != nil {
		return Settings{}, err
	}
	out := Settings{Timezone: row.Timezone}
	if row.NtfyTopic.Valid {
		out.NtfyTopic = &row.NtfyTopic.String
	}
	return out, nil
}

// Put replaces the owner's settings. They must already be validated.
func (s *Store) Put(ctx context.Context, owner uuid.UUID, v Settings) error {
	var topic pgtype.Text
	if v.NtfyTopic != nil {
		topic = pgtype.Text{String: *v.NtfyTopic, Valid: true}
	}
	err := dbgen.New(s.Pool).PutOwnerSettings(ctx, dbgen.PutOwnerSettingsParams{
		OwnerID: owner, NtfyTopic: topic, Timezone: v.Timezone,
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return ErrTopicTaken
	}
	return err
}
