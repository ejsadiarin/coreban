-- +goose Up
-- A note may also link a corefinance expense or income (ADR-0003). Those
-- live in another service, so record_id is stored unchecked: no foreign
-- key, and nothing clears it when the record is deleted. The web finds the
-- record missing instead and saves the content it last showed as the
-- note's snapshot (first one wins), which is how coreban knows it is gone.
-- A record has at most one note per owner.
ALTER TABLE notes
    ADD COLUMN record_id UUID,
    DROP CONSTRAINT notes_link_kind_check,
    ADD CONSTRAINT notes_link_kind_check CHECK (link_kind IN ('reminder', 'card', 'expense', 'income')),
    DROP CONSTRAINT notes_link_matches_kind,
    ADD CONSTRAINT notes_link_matches_kind CHECK (
        (reminder_id IS NULL OR link_kind = 'reminder') AND (card_id IS NULL OR link_kind = 'card') AND
        COALESCE(link_kind IN ('expense', 'income'), false) = (record_id IS NOT NULL));
CREATE UNIQUE INDEX notes_record_key ON notes (owner_id, link_kind, record_id) WHERE record_id IS NOT NULL;

-- +goose Down
DROP INDEX notes_record_key;
DELETE FROM notes WHERE record_id IS NOT NULL;
ALTER TABLE notes
    DROP CONSTRAINT notes_link_matches_kind,
    ADD CONSTRAINT notes_link_matches_kind CHECK (
        (reminder_id IS NULL OR link_kind = 'reminder') AND (card_id IS NULL OR link_kind = 'card')),
    DROP CONSTRAINT notes_link_kind_check,
    ADD CONSTRAINT notes_link_kind_check CHECK (link_kind IN ('reminder', 'card')),
    DROP COLUMN record_id;
