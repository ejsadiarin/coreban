-- Settings are keyed by owner_id: an owner only ever reads or writes their
-- own row.

-- name: GetOwnerSettings :one
SELECT ntfy_topic
FROM owner_settings
WHERE owner_id = $1;

-- name: PutOwnerSettings :exec
-- timezone is the owner's zone at their first save; afterwards only
-- RecordOwnerTimezone changes it.
INSERT INTO owner_settings (owner_id, ntfy_topic, timezone)
VALUES ($1, $2, $3)
ON CONFLICT (owner_id) DO UPDATE
SET ntfy_topic = EXCLUDED.ntfy_topic,
    updated_at = now();

-- name: GetOwnerTimezone :one
-- The zone coreban last saw for the owner on a token.
SELECT timezone
FROM owner_settings
WHERE owner_id = $1;

-- name: RecordOwnerTimezone :execrows
-- Records a zone a token carried for an owner who has settings; a row
-- already in that zone is left untouched.
UPDATE owner_settings
SET timezone = sqlc.arg(timezone)
WHERE owner_id = sqlc.arg(owner_id) AND timezone <> sqlc.arg(timezone);
