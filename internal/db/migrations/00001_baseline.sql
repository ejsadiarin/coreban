-- +goose Up
-- Baseline: anchors goose's version table in the coreban schema. It creates
-- no objects; tables arrive with the first feature slice.
SELECT 1;

-- +goose Down
SELECT 1;
