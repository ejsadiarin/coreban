-- +goose Up
-- The bucket and the database can't change together (ADR-0004). When an
-- attachment row goes (its note permanently deleted, or an upload never
-- posted swept), its files are queued here in the same transaction, and a
-- cleaner deletes them from the bucket, retrying until it confirms. Queued
-- files still count toward their owner's quota until they are gone.
CREATE TABLE attachment_deletions (
    key             TEXT PRIMARY KEY,
    owner_id        UUID NOT NULL,
    size_bytes      BIGINT NOT NULL CHECK (size_bytes > 0),
    attempts        INTEGER NOT NULL DEFAULT 0,
    -- When the cleaner may next try. Also a lease: claiming a file pushes
    -- it forward, so a cleaner that dies mid-delete leaves it to another.
    next_attempt_at TIMESTAMPTZ NOT NULL,
    last_error      TEXT,
    queued_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX attachment_deletions_due ON attachment_deletions (next_attempt_at);
CREATE INDEX attachment_deletions_owner ON attachment_deletions (owner_id);

-- Keys match attachment.Key and attachment.PosterKey. A file is due once
-- its upload link (attachment.UploadLinkTTL, 15 minutes) has expired:
-- deleted sooner, the owner could upload it again through the link and
-- keep a file nothing tracks.
-- +goose StatementBegin
CREATE FUNCTION queue_attachment_files() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO attachment_deletions (key, owner_id, size_bytes, next_attempt_at)
    VALUES (OLD.owner_id::text || '/' || OLD.id::text, OLD.owner_id, OLD.size_bytes,
            OLD.created_at + interval '15 minutes')
    ON CONFLICT (key) DO NOTHING;
    IF OLD.poster_bytes IS NOT NULL THEN
        INSERT INTO attachment_deletions (key, owner_id, size_bytes, next_attempt_at)
        VALUES (OLD.owner_id::text || '/' || OLD.id::text || '.poster', OLD.owner_id, OLD.poster_bytes,
                OLD.created_at + interval '15 minutes')
        ON CONFLICT (key) DO NOTHING;
    END IF;
    RETURN OLD;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER attachments_queue_files
AFTER DELETE ON attachments
FOR EACH ROW EXECUTE FUNCTION queue_attachment_files();

-- +goose Down
DROP TRIGGER attachments_queue_files ON attachments;
DROP FUNCTION queue_attachment_files();
DROP TABLE attachment_deletions;
