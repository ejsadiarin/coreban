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

-- name: LockOwnedBoard :one
-- Confirms the board is the owner's and serialises changes to its set of
-- columns (the per-status invariant) for the rest of the transaction.
SELECT id FROM boards WHERE id = $1 AND owner_id = $2 FOR UPDATE;

-- name: GetColumn :one
SELECT id, name, status, position
FROM columns
WHERE id = $1 AND board_id = $2;

-- name: NextColumnPosition :one
SELECT (COALESCE(max(position), -1) + 1)::integer
FROM columns
WHERE board_id = $1;

-- name: UpdateColumn :exec
UPDATE columns
SET name = COALESCE(sqlc.narg(name), name),
    status = COALESCE(sqlc.narg(status), status)
WHERE id = sqlc.arg(id) AND board_id = sqlc.arg(board_id);

-- name: CountStatusColumns :one
SELECT count(*) FROM columns WHERE board_id = $1 AND status = $2;

-- name: CountColumnCards :one
-- Every card counts, archived or not.
SELECT count(*) FROM cards WHERE column_id = $1;

-- name: DeleteColumn :exec
DELETE FROM columns WHERE id = $1 AND board_id = $2;

-- name: SetColumnPosition :exec
UPDATE columns SET position = $3 WHERE id = $1 AND board_id = $2;
