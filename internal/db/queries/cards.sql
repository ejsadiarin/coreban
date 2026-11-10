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
SELECT k.id, k.board_id, k.column_id, c.status, k.title, k.description, k.due_date, k.due_at, k.archived_at, k.created_at, k.updated_at
FROM cards k
JOIN columns c ON c.id = k.column_id
JOIN boards b ON b.id = k.board_id
WHERE k.id = $1 AND k.board_id = $2 AND b.owner_id = $3;

-- name: ListBoardCards :many
-- The board's cards in priority order; archived cards are left out.
SELECT k.id, k.board_id, k.column_id, c.status, k.title, k.description, k.due_date, k.due_at, k.archived_at, k.created_at, k.updated_at
FROM cards k
JOIN columns c ON c.id = k.column_id
WHERE k.board_id = $1 AND k.archived_at IS NULL
ORDER BY k.position, k.id;

-- name: ListArchivedCards :many
-- Most recently archived first.
SELECT k.id, k.board_id, k.column_id, c.status, k.title, k.description, k.due_date, k.due_at, k.archived_at, k.created_at, k.updated_at
FROM cards k
JOIN columns c ON c.id = k.column_id
WHERE k.board_id = $1 AND k.archived_at IS NOT NULL
ORDER BY k.archived_at DESC, k.id;

-- name: UpdateCard :execrows
-- set_due replaces the due (both fields; NULLs clear it), else it's kept.
UPDATE cards k
SET title = COALESCE(sqlc.narg(title), k.title),
    description = COALESCE(sqlc.narg(description), k.description),
    due_date = CASE WHEN sqlc.arg(set_due)::boolean THEN sqlc.narg(due_date)::date ELSE k.due_date END,
    due_at = CASE WHEN sqlc.arg(set_due)::boolean THEN sqlc.narg(due_at)::timestamptz ELSE k.due_at END,
    updated_at = now()
FROM boards b
WHERE k.id = sqlc.arg(id) AND k.board_id = sqlc.arg(board_id)
  AND b.id = k.board_id AND b.owner_id = sqlc.arg(owner_id);

-- name: LockOwnedCard :one
SELECT k.id, k.archived_at
FROM cards k
JOIN boards b ON b.id = k.board_id
WHERE k.id = $1 AND k.board_id = $2 AND b.owner_id = $3
FOR UPDATE OF k;

-- name: ListColumnPositions :many
-- The column's cards in order, leaving out the card being placed and
-- archived cards (a restored card returns to the bottom).
SELECT id, position
FROM cards
WHERE column_id = $1 AND id <> $2 AND archived_at IS NULL
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
    WHERE o.column_id = $1 AND o.id <> $2 AND o.archived_at IS NULL
) r
WHERE k.id = r.id;

-- name: PlaceCard :exec
UPDATE cards
SET column_id = $2, position = $3, updated_at = now()
WHERE id = $1;

-- name: ArchiveCard :exec
UPDATE cards SET archived_at = COALESCE(archived_at, now()), updated_at = now() WHERE id = $1;

-- name: RestoreCard :exec
-- Back to the bottom of its column.
UPDATE cards k
SET archived_at = NULL,
    position = COALESCE((SELECT max(o.position) FROM cards o WHERE o.column_id = k.column_id AND o.archived_at IS NULL), 0) + 1,
    updated_at = now()
WHERE k.id = $1 AND k.archived_at IS NOT NULL;

-- name: DeleteCard :execrows
DELETE FROM cards k
USING boards b
WHERE k.id = $1 AND k.board_id = $2 AND b.id = k.board_id AND b.owner_id = $3;
