-- +goose Up
-- A plain note may carry up to ten images or videos, its attachments
-- (ADR-0004). Their bytes live in object storage under keys derived from
-- the owner and attachment ids; a row here is reserved when the web asks
-- for an upload link (note_id NULL: an upload, counting toward the owner's
-- quota) and joins its note, in album order, when the note is posted.
-- Attachments are fixed once posted.
CREATE TABLE attachments (
    id           UUID PRIMARY KEY,
    owner_id     UUID NOT NULL,
    note_id      UUID REFERENCES notes (id) ON DELETE CASCADE,
    position     SMALLINT CHECK (position BETWEEN 0 AND 9),
    kind         TEXT NOT NULL CHECK (kind IN ('image', 'video')),
    content_type TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL CHECK (size_bytes > 0),
    width        INTEGER CHECK (width > 0),
    height       INTEGER CHECK (height > 0),
    poster_bytes BIGINT CHECK (poster_bytes > 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT attachments_posted_in_place CHECK ((note_id IS NULL) = (position IS NULL)),
    CONSTRAINT attachments_poster_on_video CHECK (kind = 'video' OR poster_bytes IS NULL)
);
CREATE UNIQUE INDEX attachments_note_position ON attachments (note_id, position) WHERE note_id IS NOT NULL;
CREATE INDEX attachments_owner ON attachments (owner_id);

-- A plain note now holds text, attachments, or both; a caption cleared
-- leaves body NULL. attachment_count lets the database hold that rule. A
-- linked note keeps no text and takes no attachments.
ALTER TABLE notes
    ADD COLUMN attachment_count SMALLINT NOT NULL DEFAULT 0 CHECK (attachment_count BETWEEN 0 AND 10),
    DROP CONSTRAINT notes_text_or_link,
    ADD CONSTRAINT notes_text_or_link CHECK (link_kind IS NULL OR body IS NULL),
    ADD CONSTRAINT notes_has_content CHECK (link_kind IS NOT NULL OR body IS NOT NULL OR attachment_count > 0),
    ADD CONSTRAINT notes_attachments_plain CHECK (link_kind IS NULL OR attachment_count = 0);

-- +goose Down
DROP TABLE attachments;
DELETE FROM notes WHERE link_kind IS NULL AND body IS NULL;
ALTER TABLE notes
    DROP CONSTRAINT notes_attachments_plain,
    DROP CONSTRAINT notes_has_content,
    DROP CONSTRAINT notes_text_or_link,
    ADD CONSTRAINT notes_text_or_link CHECK ((link_kind IS NULL) = (body IS NOT NULL)),
    DROP COLUMN attachment_count;
