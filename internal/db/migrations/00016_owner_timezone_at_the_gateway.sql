-- +goose Up
-- The gateway owns an owner's timezone (coregateway ADR-0005): API requests
-- read it from the internal token's tz claim, and the reminder dispatcher
-- looks it up from the gateway. coreban drops its own copy; owner settings
-- keep the notification target only.
--
-- Run coregateway's docs/runbooks/carry-over-user-timezones.md first: it
-- copies these values to the gateway, and has nothing to copy afterwards.
ALTER TABLE owner_settings DROP COLUMN timezone;

-- +goose Down
-- Every owner comes back in UTC; the zones the column held are gone.
ALTER TABLE owner_settings
    ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC' CHECK (timezone <> '');
