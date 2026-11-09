-- Settings are keyed by owner_id: an owner only ever reads or writes their
-- own row.

-- name: GetOwnerSettings :one
SELECT ntfy_topic, timezone
FROM owner_settings
WHERE owner_id = $1;

-- name: PutOwnerSettings :exec
INSERT INTO owner_settings (owner_id, ntfy_topic, timezone)
VALUES ($1, $2, $3)
ON CONFLICT (owner_id) DO UPDATE
SET ntfy_topic = EXCLUDED.ntfy_topic,
    timezone = EXCLUDED.timezone,
    updated_at = now();
