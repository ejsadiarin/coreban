-- Owner-facing queries are scoped by owner_id. The dispatcher's queries
-- (ClaimOccurrence onwards) work across owners.

-- name: LockOwnerReminders :exec
-- Serialises an owner's reminder creates for the transaction, so the cap
-- check and the insert can't interleave.
SELECT pg_advisory_xact_lock(hashtextextended('coreban.reminders:' || sqlc.arg(owner_id)::uuid::text, 0));

-- name: CountOwnerReminders :one
SELECT count(*) FROM reminders WHERE owner_id = $1;

-- name: CreateReminder :exec
INSERT INTO reminders (id, owner_id, message, starts_at, rrule, local_start, next_fire_at, next_tz, card_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetOwnedCard :one
-- A card on one of the owner's boards, with its due.
SELECT k.id, k.board_id, k.title, k.due_date, k.due_at
FROM cards k
JOIN boards b ON b.id = k.board_id
WHERE k.id = $1 AND b.owner_id = $2;

-- name: GetReminder :one
SELECT r.id, r.message, r.starts_at, r.rrule, r.local_start, r.next_fire_at, r.next_tz,
       r.last_delivery_at, r.last_delivery_late, r.last_delivery_error,
       r.created_at, r.updated_at,
       r.card_id, k.board_id AS card_board_id, k.title AS card_title,
       COALESCE(c.status = 'done', false)::boolean AS card_completed,
       COALESCE(k.archived_at IS NOT NULL, false)::boolean AS card_archived
FROM reminders r
LEFT JOIN cards k ON k.id = r.card_id
LEFT JOIN columns c ON c.id = k.column_id
WHERE r.id = $1 AND r.owner_id = $2;

-- name: ListOwnerReminders :many
SELECT r.id, r.message, r.starts_at, r.rrule, r.local_start, r.next_fire_at, r.next_tz,
       r.last_delivery_at, r.last_delivery_late, r.last_delivery_error,
       r.created_at, r.updated_at,
       r.card_id, k.board_id AS card_board_id, k.title AS card_title,
       COALESCE(c.status = 'done', false)::boolean AS card_completed,
       COALESCE(k.archived_at IS NOT NULL, false)::boolean AS card_archived
FROM reminders r
LEFT JOIN cards k ON k.id = r.card_id
LEFT JOIN columns c ON c.id = k.column_id
WHERE r.owner_id = sqlc.arg(owner_id)
  AND (sqlc.narg(card_id)::uuid IS NULL OR r.card_id = sqlc.narg(card_id))
ORDER BY r.created_at, r.id;

-- name: UpdateReminder :execrows
-- set_schedule replaces the whole schedule, re-arming the reminder.
UPDATE reminders
SET message = COALESCE(sqlc.narg(message), message),
    starts_at = CASE WHEN sqlc.arg(set_schedule)::boolean THEN sqlc.narg(starts_at)::timestamptz ELSE starts_at END,
    rrule = CASE WHEN sqlc.arg(set_schedule)::boolean THEN sqlc.narg(rrule)::text ELSE rrule END,
    local_start = CASE WHEN sqlc.arg(set_schedule)::boolean THEN sqlc.narg(local_start)::timestamp ELSE local_start END,
    next_fire_at = CASE WHEN sqlc.arg(set_schedule)::boolean THEN sqlc.narg(next_fire_at)::timestamptz ELSE next_fire_at END,
    next_tz = CASE WHEN sqlc.arg(set_schedule)::boolean THEN sqlc.narg(next_tz)::text ELSE next_tz END,
    card_id = CASE WHEN sqlc.arg(set_card)::boolean THEN sqlc.narg(card_id)::uuid ELSE card_id END,
    updated_at = now()
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- name: DeleteReminder :execrows
DELETE FROM reminders WHERE id = $1 AND owner_id = $2;

-- name: ClaimOccurrence :one
-- The earliest reminder of an owner with a notification target whose
-- moment has come, or (recurring) whose next occurrence was worked out in a
-- timezone the owner has since left. Locked for the claiming transaction;
-- rows another dispatcher holds are skipped.
-- paused: its card is completed (Done) or archived, so this occurrence is
-- skipped, not sent.
SELECT r.id, r.message, r.rrule, r.local_start, r.next_fire_at, r.next_tz, r.last_delivery_at, s.ntfy_topic, s.timezone,
       COALESCE(c.status = 'done' OR k.archived_at IS NOT NULL, false)::boolean AS paused
FROM reminders r
JOIN owner_settings s ON s.owner_id = r.owner_id
LEFT JOIN cards k ON k.id = r.card_id
LEFT JOIN columns c ON c.id = k.column_id
WHERE s.ntfy_topic IS NOT NULL
  AND (r.next_fire_at <= sqlc.arg(now) OR r.next_tz <> s.timezone)
ORDER BY r.next_fire_at, r.id
LIMIT 1
FOR UPDATE OF r SKIP LOCKED;

-- name: AdvanceReminder :exec
UPDATE reminders
SET next_fire_at = sqlc.narg(next_fire_at), next_tz = sqlc.narg(next_tz)
WHERE id = sqlc.arg(id);

-- name: RestoreReminder :exec
-- Puts back an occurrence a send could not deliver yet, unless the owner
-- has rescheduled the reminder since it was claimed.
UPDATE reminders SET next_fire_at = sqlc.arg(occurrence)
WHERE id = sqlc.arg(id) AND rrule IS NULL AND next_fire_at IS NULL;

-- name: RecordDelivery :exec
UPDATE reminders
SET last_delivery_at = sqlc.arg(at),
    last_delivery_late = sqlc.arg(late),
    last_delivery_error = sqlc.narg(error)
WHERE id = sqlc.arg(id);

-- name: EarliestFireAt :one
-- When the dispatcher next has something to send.
SELECT min(r.next_fire_at)::timestamptz
FROM reminders r
JOIN owner_settings s ON s.owner_id = r.owner_id
WHERE r.next_fire_at IS NOT NULL AND s.ntfy_topic IS NOT NULL;
