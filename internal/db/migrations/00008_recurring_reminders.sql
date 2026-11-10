-- +goose Up
-- Recurring reminders, stored the iCalendar way so CalDAV/ICS can come later
-- without migrating data: rrule is the RRULE (FREQ=DAILY, FREQ=DAILY;
-- INTERVAL=3, FREQ=WEEKLY;BYDAY=MO,WE,FR) and local_start the DTSTART as a
-- floating wall-clock time (no zone), read in the owner's timezone. So a
-- recurring reminder follows its owner when they change timezone.
-- next_fire_at is then the next occurrence as an instant, worked out in
-- next_tz; when the owner's timezone no longer matches, the dispatcher
-- works it out again. A one-off keeps only starts_at.
ALTER TABLE reminders
    ALTER COLUMN starts_at DROP NOT NULL,
    ADD COLUMN rrule       TEXT,
    ADD COLUMN local_start TIMESTAMP,
    ADD COLUMN next_tz     TEXT,
    ADD CONSTRAINT reminders_schedule_shape CHECK (
        (rrule IS NULL AND starts_at IS NOT NULL AND local_start IS NULL AND next_tz IS NULL)
        OR (rrule IS NOT NULL AND starts_at IS NULL AND local_start IS NOT NULL
            AND next_tz IS NOT NULL AND next_fire_at IS NOT NULL));

-- +goose Down
DELETE FROM reminders WHERE rrule IS NOT NULL;
ALTER TABLE reminders
    DROP CONSTRAINT reminders_schedule_shape,
    DROP COLUMN next_tz,
    DROP COLUMN local_start,
    DROP COLUMN rrule,
    ALTER COLUMN starts_at SET NOT NULL;
