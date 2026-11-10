-- +goose Up
-- A card's due is a calendar date or an instant, never both, so it reads
-- back as whichever the owner set. archived_at hides a card from its board
-- without losing it (its column still counts it, so a column with archived
-- cards can't be deleted).
ALTER TABLE cards
    ADD COLUMN due_date    DATE,
    ADD COLUMN due_at      TIMESTAMPTZ,
    ADD COLUMN archived_at TIMESTAMPTZ,
    ADD CONSTRAINT cards_due_is_date_or_instant CHECK (due_date IS NULL OR due_at IS NULL);

-- +goose Down
ALTER TABLE cards
    DROP CONSTRAINT cards_due_is_date_or_instant,
    DROP COLUMN archived_at,
    DROP COLUMN due_at,
    DROP COLUMN due_date;
