-- Owner-facing queries are scoped by owner_id. The dispatcher's queries
-- (ClaimOccurrence onwards) work across owners.

-- name: LockOwnerReminders :exec
-- Serialises an owner's reminder creates for the transaction, so the cap
-- check and the insert can't interleave.
SELECT pg_advisory_xact_lock(hashtextextended('coreban.reminders:' || sqlc.arg(owner_id)::uuid::text, 0));

-- name: CountOwnerReminders :one
SELECT count(*) FROM reminders WHERE owner_id = $1;

-- name: CreateReminder :exec
INSERT INTO reminders (id, owner_id, message, starts_at, next_fire_at)
VALUES ($1, $2, $3, $4, $4);

-- name: GetReminder :one
SELECT id, message, starts_at, next_fire_at,
       last_delivery_at, last_delivery_late, last_delivery_error,
       created_at, updated_at
FROM reminders
WHERE id = $1 AND owner_id = $2;

-- name: ListOwnerReminders :many
-- Soonest first; reminders with nothing left to send last.
SELECT id, message, starts_at, next_fire_at,
       last_delivery_at, last_delivery_late, last_delivery_error,
       created_at, updated_at
FROM reminders
WHERE owner_id = $1
ORDER BY next_fire_at NULLS LAST, created_at, id;

-- name: UpdateReminder :execrows
-- A new starts_at re-arms the reminder at that moment.
UPDATE reminders
SET message = COALESCE(sqlc.narg(message), message),
    starts_at = COALESCE(sqlc.narg(starts_at), starts_at),
    next_fire_at = CASE WHEN sqlc.narg(starts_at)::timestamptz IS NULL THEN next_fire_at
                        ELSE sqlc.narg(starts_at) END,
    updated_at = now()
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id);

-- name: DeleteReminder :execrows
DELETE FROM reminders WHERE id = $1 AND owner_id = $2;

-- name: ClaimOccurrence :one
-- The earliest occurrence whose moment has come, of an owner who has a
-- notification target,
-- locked for the claiming transaction; rows another dispatcher holds are
-- skipped.
SELECT r.id, r.message, r.next_fire_at, s.ntfy_topic, s.timezone
FROM reminders r
JOIN owner_settings s ON s.owner_id = r.owner_id
WHERE r.next_fire_at <= sqlc.arg(now) AND s.ntfy_topic IS NOT NULL
ORDER BY r.next_fire_at, r.id
LIMIT 1
FOR UPDATE OF r SKIP LOCKED;

-- name: AdvanceReminder :exec
UPDATE reminders SET next_fire_at = sqlc.narg(next_fire_at) WHERE id = sqlc.arg(id);

-- name: RestoreReminder :exec
-- Puts back an occurrence a send could not deliver yet, unless the owner
-- has rescheduled the reminder since it was claimed.
UPDATE reminders SET next_fire_at = sqlc.arg(occurrence)
WHERE id = sqlc.arg(id) AND next_fire_at IS NOT DISTINCT FROM sqlc.narg(advanced_to);

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
