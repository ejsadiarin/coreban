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
SET body = $3, archived_at = $4, archived_card = $5, updated_at = now()
WHERE id = $1 AND owner_id = $2
RETURNING *;

-- name: DeleteNote :execrows
DELETE FROM notes
WHERE id = $1 AND owner_id = $2;

-- name: CreateLinkedNote :exec
-- A note for a record just captured from the Feed, at the end of the feed.
INSERT INTO notes (id, owner_id, link_kind, reminder_id, card_id)
VALUES ($1, $2, $3, $4, $5);

-- name: LinkNote :execrows
-- Converts one of the owner's live plain notes in place: it keeps its place
-- in the feed and drops its own text for the record's.
UPDATE notes
SET body = NULL, link_kind = $3, reminder_id = $4, card_id = $5, updated_at = now()
WHERE id = $1 AND owner_id = $2 AND link_kind IS NULL AND archived_at IS NULL;

-- name: ListNoteReminders :many
-- The owner's reminders among ids, as their notes show them.
SELECT id, message, starts_at, rrule, local_start, next_fire_at, next_tz,
       last_delivery_at, last_delivery_late, last_delivery_error,
       created_at, updated_at,
       card_id, card_board_id, card_title, card_completed, card_archived
FROM note_reminders
WHERE owner_id = $1 AND id = ANY(@ids::uuid[]);

-- name: ListNoteCards :many
-- The owner's cards among ids, as their notes show them.
SELECT id, board_id, board_name, column_id, column_name, status, title, archived_at
FROM note_cards
WHERE owner_id = $1 AND id = ANY(@ids::uuid[]);

-- name: ArchiveLinkedCard :execrows
-- Archives a note's card along with the note, unless it already is.
UPDATE cards k
SET archived_at = now(), updated_at = now()
FROM boards b
WHERE k.id = $1 AND k.archived_at IS NULL AND b.id = k.board_id AND b.owner_id = $2;

-- name: DeleteLinkedReminder :exec
-- Deletes a note's reminder as the note is archived; the snapshot trigger
-- keeps its last state on the note.
DELETE FROM reminders WHERE id = $1 AND owner_id = $2;
