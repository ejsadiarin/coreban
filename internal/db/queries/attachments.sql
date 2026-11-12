-- Every attachment statement is scoped by owner_id.

-- name: LockAttachmentOwner :exec
-- Serialises one owner's reservations, so two at once can't both fit
-- under the quota.
SELECT pg_advisory_xact_lock(hashtextextended('coreban.attachments:' || sqlc.arg(owner_id)::uuid::text, 0));

-- name: AttachmentUsage :one
-- Bytes the owner stores or is uploading: posted attachments (archived
-- notes included), unposted uploads, posters, and files queued for
-- deletion that the bucket still holds.
SELECT (
    COALESCE((SELECT SUM(a.size_bytes + COALESCE(a.poster_bytes, 0)) FROM attachments a WHERE a.owner_id = sqlc.arg(owner_id)::uuid), 0)
  + COALESCE((SELECT SUM(d.size_bytes) FROM attachment_deletions d WHERE d.owner_id = sqlc.arg(owner_id)::uuid), 0)
)::bigint;

-- name: ReserveAttachment :one
INSERT INTO attachments (id, owner_id, kind, content_type, size_bytes, width, height, poster_bytes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListUploads :many
-- The owner's uploads among ids that no note has taken yet.
SELECT *
FROM attachments
WHERE owner_id = $1 AND id = ANY(@ids::uuid[]) AND note_id IS NULL;

-- name: AttachToNote :execrows
-- Gives a just-posted note its uploads, in the order the ids were given.
UPDATE attachments a
SET note_id = sqlc.arg(note_id), position = (o.ord - 1)::smallint
FROM unnest(sqlc.arg(ids)::uuid[]) WITH ORDINALITY AS o(id, ord)
WHERE a.id = o.id AND a.owner_id = sqlc.arg(owner_id) AND a.note_id IS NULL;

-- name: ListNoteAttachments :many
-- The attachments of the owner's notes among note_ids, in album order.
SELECT *
FROM attachments
WHERE owner_id = $1 AND note_id = ANY(@note_ids::uuid[])
ORDER BY note_id, position;

-- The cleaner works across owners: these are the only statements here not
-- scoped by owner_id.

-- name: SweepUploads :execrows
-- Deletes uploads no note took before cutoff; the trigger queues their
-- files.
DELETE FROM attachments
WHERE note_id IS NULL AND created_at < sqlc.arg(cutoff);

-- name: ClaimDeletions :many
-- Leases up to max_rows queued files due by now until lease_until, so no
-- other cleaner takes them meanwhile; the bucket is called after this
-- commits.
UPDATE attachment_deletions d
SET next_attempt_at = sqlc.arg(lease_until)
WHERE d.key IN (
    SELECT q.key FROM attachment_deletions q
    WHERE q.next_attempt_at <= sqlc.arg(now)
    ORDER BY q.next_attempt_at
    LIMIT sqlc.arg(max_rows)
    FOR UPDATE SKIP LOCKED
)
RETURNING d.key, d.attempts;

-- name: DeletionsDone :exec
DELETE FROM attachment_deletions WHERE key = ANY(@keys::text[]);

-- name: DeletionFailed :exec
UPDATE attachment_deletions
SET attempts = attempts + 1, next_attempt_at = sqlc.arg(next_attempt_at), last_error = sqlc.arg(last_error)
WHERE key = sqlc.arg(key);

-- name: NextDeletion :one
-- When the next queued file is due, if any is queued.
SELECT MIN(next_attempt_at)::timestamptz FROM attachment_deletions;
