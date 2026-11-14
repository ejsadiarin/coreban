-- +goose Up
-- Every budget record gets a note, placed on the record's own date
-- (ADR-0005). posted_at is where a note sits in the feed: when it was
-- posted, or for a budget record the record's date. Existing notes keep
-- their creation time. origin tells notes posted from the Feed ('feed')
-- from notes the web's sync created for budget records ('budget'); only
-- the latter go away with their record.
ALTER TABLE notes
    ADD COLUMN posted_at timestamptz,
    ADD COLUMN origin text NOT NULL DEFAULT 'feed' CHECK (origin IN ('feed', 'budget'));
UPDATE notes SET posted_at = created_at;
ALTER TABLE notes
    ALTER COLUMN posted_at SET NOT NULL,
    ALTER COLUMN posted_at SET DEFAULT now();

-- The feed pages by (posted_at, seq), newest first; seq breaks ties.
DROP INDEX notes_owner_id_seq_idx;
CREATE INDEX notes_owner_posted_idx ON notes (owner_id, posted_at DESC, seq DESC);

-- +goose Down
DROP INDEX notes_owner_posted_idx;
CREATE INDEX notes_owner_id_seq_idx ON notes (owner_id, seq DESC);
ALTER TABLE notes DROP COLUMN origin, DROP COLUMN posted_at;
