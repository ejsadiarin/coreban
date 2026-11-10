-- Label statements take a board_id the caller has already resolved through
-- an owner-scoped query (GetBoard), and are scoped by it.

-- name: ListLabels :many
SELECT id, name FROM labels WHERE board_id = $1 ORDER BY lower(name), id;

-- name: CreateLabel :exec
INSERT INTO labels (id, board_id, name) VALUES ($1, $2, $3);

-- name: RenameLabel :execrows
UPDATE labels SET name = $3 WHERE id = $1 AND board_id = $2;

-- name: DeleteLabel :execrows
DELETE FROM labels WHERE id = $1 AND board_id = $2;

-- name: LabelOnBoard :one
SELECT id FROM labels WHERE id = $1 AND board_id = $2;

-- name: CardOnBoard :one
SELECT id FROM cards WHERE id = $1 AND board_id = $2;

-- name: ApplyLabel :exec
INSERT INTO card_labels (card_id, label_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: RemoveLabel :exec
DELETE FROM card_labels WHERE card_id = $1 AND label_id = $2;

-- name: ListBoardCardLabels :many
-- Every card's labels on the board, each card's by name.
SELECT cl.card_id, l.id, l.name
FROM card_labels cl
JOIN labels l ON l.id = cl.label_id
WHERE l.board_id = $1
ORDER BY cl.card_id, lower(l.name), l.id;
