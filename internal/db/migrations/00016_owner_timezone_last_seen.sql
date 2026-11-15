-- +goose Up
-- The gateway owns an owner's timezone (coregateway ADR-0005) and asserts it
-- in each internal token's tz claim. coreban keeps owner_settings.timezone,
-- now as the zone it last saw for the owner on a token: requests without a
-- tz claim and the reminder dispatcher (which has no token) read it. It is
-- no longer a setting the owner saves here. No data changes.
COMMENT ON COLUMN owner_settings.timezone IS
    'The IANA zone coreban last saw for this owner in a gateway token''s tz claim (coregateway ADR-0005); not set by the owner here.';

-- +goose Down
COMMENT ON COLUMN owner_settings.timezone IS NULL;
