-- Every note statement is scoped by owner_id, so an owner can never read or
-- change another owner's note.

-- name: CreateNote :one
-- A plain note: text, attachments (counted here, attached by
-- AttachToNote in the same transaction), or both.
INSERT INTO notes (id, owner_id, body, attachment_count)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListNotes :many
-- One page of the feed going back in time: the newest notes placed before
-- the cursor (posted_at, then seq), newest first. scope narrows it to
-- 'budget' (expense and income notes) or 'notes' (everything else).
-- Archived notes are left out unless with_archived. The
-- caller asks for one more than it shows to learn whether an older page
-- exists.
SELECT *
FROM notes
WHERE owner_id = sqlc.arg(owner_id)
  AND (sqlc.narg(before_posted_at)::timestamptz IS NULL
       OR (posted_at, seq) < (sqlc.narg(before_posted_at)::timestamptz, sqlc.narg(before_seq)::bigint))
  AND (sqlc.narg(scope)::text IS NULL
       OR (sqlc.narg(scope)::text = 'budget') = COALESCE(link_kind IN ('expense', 'income'), false))
  AND (sqlc.arg(with_archived)::boolean OR archived_at IS NULL)
ORDER BY posted_at DESC, seq DESC
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

-- name: DeleteNote :one
-- Returns how many attachments the note had: their files are now queued
-- for deletion.
DELETE FROM notes
WHERE id = $1 AND owner_id = $2
RETURNING attachment_count;

-- name: CreateLinkedNote :exec
-- A note for a record just captured from the Feed, at the end of the feed.
INSERT INTO notes (id, owner_id, link_kind, reminder_id, card_id)
VALUES ($1, $2, $3, $4, $5);

-- name: LinkNote :execrows
-- Converts one of the owner's live plain notes in place: it keeps its place
-- in the feed and drops its own text for the record's. A note with
-- attachments stays a plain note.
UPDATE notes
SET body = NULL, link_kind = $3, reminder_id = $4, card_id = $5, updated_at = now()
WHERE id = $1 AND owner_id = $2 AND link_kind IS NULL AND archived_at IS NULL AND attachment_count = 0;

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

-- name: CreateRecordNote :one
-- A note for an expense or income just captured from the Feed in
-- corefinance, at the end of the feed. record_id is not checked: coreban
-- can't call corefinance.
INSERT INTO notes (id, owner_id, link_kind, record_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: SaveRecordSnapshot :exec
-- Keeps what the web last showed of a cross-service record it found gone.
-- The first snapshot wins.
UPDATE notes
SET snapshot = $3, updated_at = now()
WHERE id = $1 AND owner_id = $2 AND record_id IS NOT NULL AND snapshot IS NULL;

-- name: SyncRecordNotes :many
-- Gives each budget record its note at posted_at, unless it has one, in
-- one statement. An existing note moves to posted_at only when its local
-- date (in tz) isn't posted_at's, which is the record's date, so it keeps
-- its time of day until the record is re-dated. One row per note created
-- or moved; inserted tells which. The records must be distinct.
INSERT INTO notes (id, owner_id, link_kind, record_id, posted_at, origin)
SELECT r.id, sqlc.arg(owner_id), r.kind, r.record_id, r.posted_at, 'budget'
FROM (
    -- Equal-length arrays unnest side by side, one row per record.
    SELECT unnest(sqlc.arg(ids)::uuid[]) AS id, unnest(sqlc.arg(kinds)::text[]) AS kind,
           unnest(sqlc.arg(record_ids)::uuid[]) AS record_id,
           unnest(sqlc.arg(posted_ats)::timestamptz[]) AS posted_at
) r
ON CONFLICT (owner_id, link_kind, record_id) WHERE record_id IS NOT NULL
DO UPDATE SET posted_at = EXCLUDED.posted_at, updated_at = now()
WHERE (notes.posted_at AT TIME ZONE sqlc.arg(tz)::text)::date
   <> (EXCLUDED.posted_at AT TIME ZONE sqlc.arg(tz)::text)::date
RETURNING (xmax = 0) AS inserted;

-- name: DeleteRecordNote :one
-- Deletes the note of a budget record, if the sync made it. Returns the
-- origin of the note found, so a note posted from the Feed can be told
-- apart: it is kept (ADR-0003).
WITH found AS (
    SELECT n.id, n.origin FROM notes n
    WHERE n.owner_id = $1 AND n.link_kind = $2 AND n.record_id = $3
), gone AS (
    DELETE FROM notes d USING found f
    WHERE d.id = f.id AND f.origin = 'budget'
)
SELECT origin FROM found;
