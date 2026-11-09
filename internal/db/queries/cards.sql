-- Card statements take a board_id the caller has already resolved through
-- an owner-scoped query (LockOwnedColumn, GetBoard), or join boards to
-- check owner_id themselves. A card's status is always read from its column.

-- name: LockOwnedColumn :one
-- Confirms the column is on the owner's board and serialises card
-- placement within it for the rest of the transaction.
SELECT c.id
FROM columns c
JOIN boards b ON b.id = c.board_id
WHERE c.id = $1 AND c.board_id = $2 AND b.owner_id = $3
FOR UPDATE OF c;

-- name: CreateCard :exec
INSERT INTO cards (id, board_id, column_id, title, position)
VALUES ($1, $2, $3, $4,
        COALESCE((SELECT max(position) FROM cards WHERE column_id = $3), 0) + 1);

-- name: GetCard :one
SELECT k.id, k.board_id, k.column_id, c.status, k.title, k.description, k.created_at, k.updated_at
FROM cards k
JOIN columns c ON c.id = k.column_id
JOIN boards b ON b.id = k.board_id
WHERE k.id = $1 AND k.board_id = $2 AND b.owner_id = $3;

-- name: ListBoardCards :many
SELECT k.id, k.board_id, k.column_id, c.status, k.title, k.description, k.created_at, k.updated_at
FROM cards k
JOIN columns c ON c.id = k.column_id
WHERE k.board_id = $1
ORDER BY k.position, k.id;

-- name: UpdateCard :execrows
UPDATE cards k
SET title = COALESCE(sqlc.narg(title), k.title),
    description = COALESCE(sqlc.narg(description), k.description),
    updated_at = now()
FROM boards b
WHERE k.id = sqlc.arg(id) AND k.board_id = sqlc.arg(board_id)
  AND b.id = k.board_id AND b.owner_id = sqlc.arg(owner_id);

-- name: LockOwnedCard :one
SELECT k.id
FROM cards k
JOIN boards b ON b.id = k.board_id
WHERE k.id = $1 AND k.board_id = $2 AND b.owner_id = $3
FOR UPDATE OF k;

-- name: ListColumnPositions :many
-- The column's cards in order, leaving out the card being placed.
SELECT id, position
FROM cards
WHERE column_id = $1 AND id <> $2
ORDER BY position, id;

-- name: RenumberColumn :exec
-- Spreads the column's cards (except the one being placed) back to 1..n
-- in their current order. Only needed when repeated inserts between the
-- same neighbours exhaust the gap between them.
UPDATE cards k
SET position = r.ord
FROM (
    SELECT o.id, row_number() OVER (ORDER BY o.position, o.id) AS ord
    FROM cards o
    WHERE o.column_id = $1 AND o.id <> $2
) r
WHERE k.id = r.id;

-- name: PlaceCard :exec
UPDATE cards
SET column_id = $2, position = $3, updated_at = now()
WHERE id = $1;
