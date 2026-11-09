-- +goose Up
-- Boards belong to an owner: the gateway token's subject, stored opaquely
-- with no foreign key and no copied user data.
CREATE TABLE boards (
    id         UUID PRIMARY KEY,
    owner_id   UUID NOT NULL,
    name       TEXT NOT NULL CHECK (btrim(name) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX boards_owner_id_created_at_idx ON boards (owner_id, created_at);

-- ADR-0001: every column belongs to exactly one status from the fixed set;
-- a card's status is derived from its column, never stored on the card.
CREATE TABLE columns (
    id         UUID PRIMARY KEY,
    board_id   UUID NOT NULL REFERENCES boards (id) ON DELETE CASCADE,
    name       TEXT NOT NULL CHECK (btrim(name) <> ''),
    status     TEXT NOT NULL CHECK (status IN ('todo', 'in_progress', 'done')),
    position   INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (board_id, position) DEFERRABLE INITIALLY IMMEDIATE
);

-- +goose Down
DROP TABLE columns;
DROP TABLE boards;
