-- +goose Up
-- A card sits in exactly one column. Its status is derived from that column
-- (ADR-0001) and never stored here. position orders cards within a column
-- (position is priority): a move takes the midpoint between its new
-- neighbours, so reordering rewrites one row instead of the whole column.
-- A column holding cards cannot be deleted (RESTRICT); deleting the board
-- takes its cards with it.
CREATE TABLE cards (
    id          UUID PRIMARY KEY,
    board_id    UUID NOT NULL REFERENCES boards (id) ON DELETE CASCADE,
    column_id   UUID NOT NULL REFERENCES columns (id) ON DELETE RESTRICT,
    title       TEXT NOT NULL CHECK (btrim(title) <> ''),
    description TEXT NOT NULL DEFAULT '',
    position    DOUBLE PRECISION NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX cards_column_id_position_idx ON cards (column_id, position);
CREATE INDEX cards_board_id_idx ON cards (board_id);

-- +goose Down
DROP TABLE cards;
