-- +goose Up
-- A reminder pushes its message to the owner's notification target on a
-- schedule. starts_at is the schedule's iCalendar DTSTART: a one-off fires
-- exactly then (recurring presets will add an RRULE beside it, not a new
-- shape). next_fire_at is the occurrence still to be sent, NULL once none
-- is left; the dispatcher advances it before it sends, so an occurrence is
-- sent at most once (ADR-0002). last_delivery_* records the latest send.
CREATE TABLE reminders (
    id                  UUID PRIMARY KEY,
    owner_id            UUID NOT NULL,
    message             TEXT NOT NULL CHECK (btrim(message) <> ''),
    starts_at           TIMESTAMPTZ NOT NULL,
    next_fire_at        TIMESTAMPTZ,
    last_delivery_at    TIMESTAMPTZ,
    last_delivery_late  BOOLEAN NOT NULL DEFAULT false,
    last_delivery_error TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX reminders_owner_id_idx ON reminders (owner_id, created_at);
CREATE INDEX reminders_next_fire_at_idx ON reminders (next_fire_at) WHERE next_fire_at IS NOT NULL;

-- +goose Down
DROP TABLE reminders;
