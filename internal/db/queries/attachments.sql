-- Every attachment statement is scoped by owner_id.

-- name: LockAttachmentOwner :exec
-- Serialises one owner's reservations, so two at once can't both fit
-- under the quota.
SELECT pg_advisory_xact_lock(hashtextextended('coreban.attachments:' || sqlc.arg(owner_id)::uuid::text, 0));

-- name: AttachmentUsage :one
-- Bytes the owner stores or is uploading: posted attachments (archived
-- notes included), unposted uploads, and posters.
SELECT COALESCE(SUM(size_bytes + COALESCE(poster_bytes, 0)), 0)::bigint
FROM attachments
WHERE owner_id = $1;

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
