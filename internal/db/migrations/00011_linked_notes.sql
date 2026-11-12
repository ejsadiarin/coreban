-- +goose Up
-- A note may link one reminder or card (ADR-0003). A linked note keeps no
-- text of its own: it shows the record as it is now, read through the
-- note_reminders and note_cards views. A record has at most one note.
-- Deleting the record clears the link (ON DELETE SET NULL) but keeps
-- link_kind, so the note reads as "(deleted)"; just before that, a trigger
-- saves the record's last view row as the note's snapshot, whichever way
-- it was deleted (directly, with its card, or with its board).
-- archived_card remembers that archiving the note archived its card, so
-- restoring the note restores the card too.
ALTER TABLE notes
    ALTER COLUMN body DROP NOT NULL,
    ADD COLUMN link_kind     TEXT CHECK (link_kind IN ('reminder', 'card')),
    ADD COLUMN reminder_id   UUID REFERENCES reminders (id) ON DELETE SET NULL,
    ADD COLUMN card_id       UUID REFERENCES cards (id) ON DELETE SET NULL,
    ADD COLUMN snapshot      JSONB,
    ADD COLUMN archived_card BOOLEAN NOT NULL DEFAULT false,
    ADD CONSTRAINT notes_text_or_link CHECK ((link_kind IS NULL) = (body IS NOT NULL)),
    ADD CONSTRAINT notes_link_matches_kind CHECK (
        (reminder_id IS NULL OR link_kind = 'reminder') AND (card_id IS NULL OR link_kind = 'card'));
CREATE UNIQUE INDEX notes_reminder_id_key ON notes (reminder_id) WHERE reminder_id IS NOT NULL;
CREATE UNIQUE INDEX notes_card_id_key ON notes (card_id) WHERE card_id IS NOT NULL;

-- A reminder as a note shows it: the columns reminder rendering reads.
CREATE VIEW note_reminders AS
SELECT r.owner_id, r.id, r.message, r.starts_at, r.rrule, r.local_start, r.next_fire_at, r.next_tz,
       r.last_delivery_at, r.last_delivery_late, r.last_delivery_error,
       r.created_at, r.updated_at,
       r.card_id, k.board_id AS card_board_id, k.title AS card_title,
       COALESCE(c.status = 'done', false)::boolean AS card_completed,
       COALESCE(k.archived_at IS NOT NULL, false)::boolean AS card_archived
FROM reminders r
LEFT JOIN cards k ON k.id = r.card_id
LEFT JOIN columns c ON c.id = k.column_id;

-- A card as a note shows it: its title, where it is, and its status.
CREATE VIEW note_cards AS
SELECT b.owner_id, k.id, k.board_id, b.name AS board_name, k.column_id, c.name AS column_name,
       c.status, k.title, k.archived_at
FROM cards k
JOIN boards b ON b.id = k.board_id
JOIN columns c ON c.id = k.column_id;

-- Saves the last view row of every linked record a delete is about to
-- take, before ON DELETE SET NULL clears the link. A board takes its
-- cards and their reminders, and a card takes its reminders, through
-- cascades that run once the parent row is gone, so each parent saves
-- them while their joins still resolve. A snapshot is never overwritten.
-- +goose StatementBegin
CREATE FUNCTION snapshot_linked_notes() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'boards' THEN
        UPDATE notes n SET snapshot = to_jsonb(v)
        FROM note_cards v
        WHERE n.card_id = v.id AND v.board_id = OLD.id AND n.snapshot IS NULL;
        UPDATE notes n SET snapshot = to_jsonb(v)
        FROM note_reminders v
        WHERE n.reminder_id = v.id AND v.card_board_id = OLD.id AND n.snapshot IS NULL;
    ELSIF TG_TABLE_NAME = 'cards' THEN
        UPDATE notes n SET snapshot = to_jsonb(v)
        FROM note_cards v
        WHERE n.card_id = v.id AND v.id = OLD.id AND n.snapshot IS NULL;
        UPDATE notes n SET snapshot = to_jsonb(v)
        FROM note_reminders v
        WHERE n.reminder_id = v.id AND v.card_id = OLD.id AND n.snapshot IS NULL;
    ELSE
        UPDATE notes n SET snapshot = to_jsonb(v)
        FROM note_reminders v
        WHERE n.reminder_id = v.id AND v.id = OLD.id AND n.snapshot IS NULL;
    END IF;
    RETURN OLD;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER boards_snapshot_linked_notes BEFORE DELETE ON boards
    FOR EACH ROW EXECUTE FUNCTION snapshot_linked_notes();
CREATE TRIGGER cards_snapshot_linked_notes BEFORE DELETE ON cards
    FOR EACH ROW EXECUTE FUNCTION snapshot_linked_notes();
CREATE TRIGGER reminders_snapshot_linked_notes BEFORE DELETE ON reminders
    FOR EACH ROW EXECUTE FUNCTION snapshot_linked_notes();

-- +goose Down
DROP TRIGGER reminders_snapshot_linked_notes ON reminders;
DROP TRIGGER cards_snapshot_linked_notes ON cards;
DROP TRIGGER boards_snapshot_linked_notes ON boards;
DROP FUNCTION snapshot_linked_notes();
DROP VIEW note_cards;
DROP VIEW note_reminders;
DELETE FROM notes WHERE link_kind IS NOT NULL;
ALTER TABLE notes
    DROP CONSTRAINT notes_link_matches_kind,
    DROP CONSTRAINT notes_text_or_link,
    DROP COLUMN archived_card,
    DROP COLUMN snapshot,
    DROP COLUMN card_id,
    DROP COLUMN reminder_id,
    DROP COLUMN link_kind,
    ALTER COLUMN body SET NOT NULL;
