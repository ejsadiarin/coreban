-- +goose Up
-- A note is an item in its owner's feed. This slice has plain notes only:
-- free text the owner posted. seq orders the feed (newest last) and is the
-- paging cursor: it only grows, so a note posted while the owner scrolls
-- back never lands inside the older pages they are reading. archived_at
-- collapses a note in the feed without losing it; deleting the row is the
-- only permanent removal. The text checks repeat the API's rules.
CREATE TABLE notes (
    id          UUID PRIMARY KEY,
    owner_id    UUID NOT NULL,
    seq         BIGINT GENERATED ALWAYS AS IDENTITY NOT NULL UNIQUE,
    body        TEXT NOT NULL CHECK (btrim(body) <> '' AND char_length(body) <= 4000),
    archived_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX notes_owner_id_seq_idx ON notes (owner_id, seq DESC);

-- +goose Down
DROP TABLE notes;
