-- +goose Up
-- A label is defined on one board and applied to that board's cards. Names
-- are unique within a board, ignoring case. Deleting a label detaches it
-- from its cards; deleting a card or board takes its labelling with it.
CREATE TABLE labels (
    id         UUID PRIMARY KEY,
    board_id   UUID NOT NULL REFERENCES boards (id) ON DELETE CASCADE,
    name       TEXT NOT NULL CHECK (btrim(name) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX labels_board_id_name_key ON labels (board_id, lower(name));

CREATE TABLE card_labels (
    card_id  UUID NOT NULL REFERENCES cards (id) ON DELETE CASCADE,
    label_id UUID NOT NULL REFERENCES labels (id) ON DELETE CASCADE,
    PRIMARY KEY (card_id, label_id)
);
CREATE INDEX card_labels_label_id_idx ON card_labels (label_id);

-- +goose Down
DROP TABLE card_labels;
DROP TABLE labels;
