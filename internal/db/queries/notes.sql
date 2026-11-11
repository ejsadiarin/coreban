-- Every note statement is scoped by owner_id, so an owner can never read or
-- change another owner's note.

-- name: CreateNote :one
INSERT INTO notes (id, owner_id, body)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListNotes :many
-- One page of the feed going back in time: the newest notes older than
-- the cursor (seq below before_seq), newest first. The caller asks for one
-- more than it shows to learn whether an older page exists.
SELECT *
FROM notes
WHERE owner_id = sqlc.arg(owner_id)
  AND (sqlc.narg(before_seq)::bigint IS NULL OR seq < sqlc.narg(before_seq)::bigint)
ORDER BY seq DESC
LIMIT sqlc.arg(row_limit);

-- name: GetNoteForUpdate :one
SELECT *
FROM notes
WHERE id = $1 AND owner_id = $2
FOR UPDATE;

-- name: UpdateNote :one
UPDATE notes
SET body = $3, archived_at = $4, updated_at = now()
WHERE id = $1 AND owner_id = $2
RETURNING *;

-- name: DeleteNote :execrows
DELETE FROM notes
WHERE id = $1 AND owner_id = $2;
