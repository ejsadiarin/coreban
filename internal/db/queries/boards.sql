-- Every board statement is scoped by owner_id, so an owner can never read
-- or change another owner's board. Column statements take a board_id the
-- caller has already resolved through an owner-scoped board query.

-- name: CreateBoard :one
INSERT INTO boards (id, owner_id, name)
VALUES ($1, $2, $3)
RETURNING created_at, updated_at;

-- name: ListBoards :many
SELECT id, name, created_at, updated_at
FROM boards
WHERE owner_id = $1
ORDER BY created_at, id;

-- name: GetBoard :one
SELECT name, created_at, updated_at
FROM boards
WHERE id = $1 AND owner_id = $2;

-- name: RenameBoard :one
UPDATE boards
SET name = $3, updated_at = now()
WHERE id = $1 AND owner_id = $2
RETURNING created_at, updated_at;

-- name: DeleteBoard :execrows
DELETE FROM boards
WHERE id = $1 AND owner_id = $2;

-- name: CreateColumn :exec
INSERT INTO columns (id, board_id, name, status, position)
VALUES ($1, $2, $3, $4, $5);

-- name: ListColumns :many
SELECT id, name, status, position
FROM columns
WHERE board_id = $1
ORDER BY position;
