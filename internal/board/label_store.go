package board

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	dbgen "github.com/ejsadiarin/coreban/internal/db/sqlc"
)

// ErrLabelNotFound is returned for a label that is not on the board.
var ErrLabelNotFound = errors.New("label not found")

// LabelTakenError is returned when the board already has a label with the
// name (ignoring case).
type LabelTakenError struct{ Name string }

func (e *LabelTakenError) Error() string {
	return fmt.Sprintf("this board already has a label named %q", e.Name)
}

// uniqueViolation is Postgres's SQLSTATE for a unique constraint breach.
const uniqueViolation = "23505"

// labelTaken turns a unique violation on the label name into
// LabelTakenError.
func labelTaken(err error, name string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return &LabelTakenError{Name: name}
	}
	return err
}

// ownedBoard confirms the board is the owner's.
func ownedBoard(ctx context.Context, q *dbgen.Queries, owner, boardID uuid.UUID) error {
	_, err := q.GetBoard(ctx, dbgen.GetBoardParams{ID: boardID, OwnerID: owner})
	return notFound(err)
}

// attachLabels sets each card's labels from the board's labelling.
func attachLabels(ctx context.Context, q *dbgen.Queries, boardID uuid.UUID, cards []Card) error {
	if len(cards) == 0 {
		return nil
	}
	rows, err := q.ListBoardCardLabels(ctx, boardID)
	if err != nil {
		return err
	}
	byCard := map[uuid.UUID][]Label{}
	for _, r := range rows {
		byCard[r.CardID] = append(byCard[r.CardID], Label{ID: r.ID, Name: r.Name})
	}
	for i := range cards {
		if ls := byCard[cards[i].ID]; ls != nil {
			cards[i].Labels = ls
		}
	}
	return nil
}

// ListLabels returns the board's labels by name.
func (s *Store) ListLabels(ctx context.Context, owner, boardID uuid.UUID) ([]Label, error) {
	q := s.queries()
	if err := ownedBoard(ctx, q, owner, boardID); err != nil {
		return nil, err
	}
	rows, err := q.ListLabels(ctx, boardID)
	if err != nil {
		return nil, err
	}
	out := make([]Label, len(rows))
	for i, r := range rows {
		out[i] = Label{ID: r.ID, Name: r.Name}
	}
	return out, nil
}

// CreateLabel defines a label on one of the owner's boards.
func (s *Store) CreateLabel(ctx context.Context, owner, boardID uuid.UUID, name string) (Label, error) {
	q := s.queries()
	if err := ownedBoard(ctx, q, owner, boardID); err != nil {
		return Label{}, err
	}
	l := Label{ID: uuid.New(), Name: name}
	return l, labelTaken(q.CreateLabel(ctx, dbgen.CreateLabelParams{ID: l.ID, BoardID: boardID, Name: name}), name)
}

// RenameLabel renames one of the board's labels.
func (s *Store) RenameLabel(ctx context.Context, owner, boardID, id uuid.UUID, name string) (Label, error) {
	q := s.queries()
	if err := ownedBoard(ctx, q, owner, boardID); err != nil {
		return Label{}, err
	}
	n, err := q.RenameLabel(ctx, dbgen.RenameLabelParams{ID: id, BoardID: boardID, Name: name})
	if err != nil {
		return Label{}, labelTaken(err, name)
	}
	if n == 0 {
		return Label{}, ErrLabelNotFound
	}
	return Label{ID: id, Name: name}, nil
}

// DeleteLabel deletes one of the board's labels, detaching it from its
// cards.
func (s *Store) DeleteLabel(ctx context.Context, owner, boardID, id uuid.UUID) error {
	q := s.queries()
	if err := ownedBoard(ctx, q, owner, boardID); err != nil {
		return err
	}
	n, err := q.DeleteLabel(ctx, dbgen.DeleteLabelParams{ID: id, BoardID: boardID})
	if err == nil && n == 0 {
		return ErrLabelNotFound
	}
	return err
}

// SetCardLabel applies (on) or removes a board label on one of its cards.
// Both are idempotent; a label from another board is ErrLabelNotFound.
func (s *Store) SetCardLabel(ctx context.Context, owner, boardID, cardID, labelID uuid.UUID, on bool) error {
	q := s.queries()
	if err := ownedBoard(ctx, q, owner, boardID); err != nil {
		return err
	}
	if _, err := q.CardOnBoard(ctx, dbgen.CardOnBoardParams{ID: cardID, BoardID: boardID}); errors.Is(err, pgx.ErrNoRows) {
		return ErrCardNotFound
	} else if err != nil {
		return err
	}
	if _, err := q.LabelOnBoard(ctx, dbgen.LabelOnBoardParams{ID: labelID, BoardID: boardID}); errors.Is(err, pgx.ErrNoRows) {
		return ErrLabelNotFound
	} else if err != nil {
		return err
	}
	if on {
		return q.ApplyLabel(ctx, dbgen.ApplyLabelParams{CardID: cardID, LabelID: labelID})
	}
	return q.RemoveLabel(ctx, dbgen.RemoveLabelParams{CardID: cardID, LabelID: labelID})
}
