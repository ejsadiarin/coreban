-- +goose Up
-- An owner's notification target (an ntfy topic on the homelab's own ntfy
-- server, never a URL) and the timezone their schedules are read in. No row
-- means no target and UTC. The topic check repeats the API's rule so no
-- write path can store a topic outside coreban's prefix ({1,56}: ntfy's
-- 64-character limit less the 8 of 'coreban-'). A topic belongs
-- to one owner, so nobody can point their reminders at someone else's
-- phone.
CREATE TABLE owner_settings (
    owner_id   UUID PRIMARY KEY,
    ntfy_topic TEXT UNIQUE CHECK (ntfy_topic ~ '^coreban-[A-Za-z0-9_-]{1,56}$'),
    timezone   TEXT NOT NULL CHECK (timezone <> ''),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE owner_settings;
