-- Owner-facing queries are scoped by owner_id. The dispatcher's queries
-- (ClaimOccurrence onwards) work across owners.

-- name: LockOwnerReminders :exec
-- Serialises an owner's reminder creates for the transaction, so the cap
-- check and the insert can't interleave.
SELECT pg_advisory_xact_lock(hashtextextended('coreban.reminders:' || sqlc.arg(owner_id)::uuid::text, 0));

-- name: CountOwnerReminders :one
SELECT count(*) FROM reminders WHERE owner_id = $1;

-- name: CreateReminder :exec
INSERT INTO reminders (id, owner_id, message, starts_at, rrule, local_start, next_fire_at, next_tz)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetReminder :one
SELECT id, message, starts_at, rrule, local_start, next_fire_at, next_tz,
       last_delivery_at, last_delivery_late, last_delivery_error,
       created_at, updated_at
FROM reminders
WHERE id = $1 AND owner_id = $2;

-- name: ListOwnerReminders :many
SELECT id, message, starts_at, rrule, local_start, next_fire_at, next_tz,
       last_delivery_at, last_delivery_late, last_delivery_error,
       created_at, updated_at
FROM reminders
WHERE owner_id = $1
ORDER BY created_at, id;

-- name: UpdateReminder :execrows
-- set_schedule replaces the whole schedule, re-arming the reminder.
UPDATE reminders
SET message = COALESCE(sqlc.narg(message), message),
    starts_at = CASE WHEN sqlc.arg(set_schedule)::boolean THEN sqlc.narg(starts_at)::timestamptz ELSE starts_at END,
    rrule = CASE WHEN sqlc.arg(set_schedule)::boolean THEN sqlc.narg(rrule)::text ELSE rrule END,
    local_start = CASE WHEN sqlc.arg(set_schedule)::boolean THEN sqlc.narg(local_start)::timestamp ELSE local_start END,
    next_fire_at = CASE WHEN sqlc.arg(set_schedule)::boolean THEN sqlc.narg(next_fire_at)::timestamptz ELSE next_fire_at END,
    next_tz = CASE WHEN sqlc.arg(set_schedule)::boolean THEN sqlc.narg(next_tz)::text ELSE next_tz END,
    updated_at = now()
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- name: DeleteReminder :execrows
DELETE FROM reminders WHERE id = $1 AND owner_id = $2;

-- name: ClaimOccurrence :one
-- The earliest reminder of an owner with a notification target whose
-- moment has come, or (recurring) whose next occurrence was worked out in a
-- timezone the owner has since left. Locked for the claiming transaction;
-- rows another dispatcher holds are skipped.
SELECT r.id, r.message, r.rrule, r.local_start, r.next_fire_at, r.next_tz, r.last_delivery_at, s.ntfy_topic, s.timezone
FROM reminders r
JOIN owner_settings s ON s.owner_id = r.owner_id
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
