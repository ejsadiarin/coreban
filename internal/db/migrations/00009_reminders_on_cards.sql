-- +goose Up
-- A reminder may be attached to a card on its owner's board; deleting the
-- card deletes its reminders. While the card is completed (status Done) or
-- archived, the dispatcher skips the reminder's occurrences without
-- deleting it.
ALTER TABLE reminders ADD COLUMN card_id UUID REFERENCES cards (id) ON DELETE CASCADE;
CREATE INDEX reminders_card_id_idx ON reminders (card_id) WHERE card_id IS NOT NULL;

-- +goose Down
ALTER TABLE reminders DROP COLUMN card_id;
