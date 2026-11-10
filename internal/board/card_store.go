package board

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/ejsadiarin/coreban/internal/db/sqlc"
)

// ErrCardNotFound is returned when a card does not exist on the owner's
// board. Like ErrNotFound, it never distinguishes "missing" from "someone
// else's".
var ErrCardNotFound = errors.New("card not found")

// ErrColumnNotFound is returned for a column that is not on the board: one
// a card names, or one being changed.
var ErrColumnNotFound = errors.New("column not found on this board")

// ErrCardArchived is returned for a move of an archived card.
var ErrCardArchived = errors.New("card is archived")

func cardFromRow(r dbgen.GetCardRow) Card {
	c := Card{
		ID: r.ID, BoardID: r.BoardID, ColumnID: r.ColumnID, Status: Status(r.Status),
		Title: r.Title, Description: r.Description, Labels: []Label{},
		CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time,
	}
	switch {
	case r.DueDate.Valid:
		c.Due = &Due{Date: r.DueDate.Time.Format(dateLayout)}
	case r.DueAt.Valid:
		at := r.DueAt.Time
		c.Due = &Due{At: &at}
	}
	if r.ArchivedAt.Valid {
		at := r.ArchivedAt.Time
		c.ArchivedAt = &at
	}
	return c
}

// lockColumn confirms column is on the owner's board and holds it for the
// transaction, so concurrent placements in it are serialised. A board the
// owner can't see answers ErrNotFound; a column not on it ErrColumnNotFound.
func lockColumn(ctx context.Context, q *dbgen.Queries, owner, boardID, column uuid.UUID) error {
	_, err := q.LockOwnedColumn(ctx, dbgen.LockOwnedColumnParams{ID: column, BoardID: boardID, OwnerID: owner})
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err := q.GetBoard(ctx, dbgen.GetBoardParams{ID: boardID, OwnerID: owner}); err != nil {
		return notFound(err)
	}
	return ErrColumnNotFound
}

// CreateCard adds a card with a title at the bottom of a column on one of
// the owner's boards.
func (s *Store) CreateCard(ctx context.Context, owner, boardID, column uuid.UUID, title string) (Card, error) {
	var c Card
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		if err := lockColumn(ctx, q, owner, boardID, column); err != nil {
			return err
		}
		id := uuid.New()
		if err := q.CreateCard(ctx, dbgen.CreateCardParams{ID: id, BoardID: boardID, ColumnID: column, Title: title}); err != nil {
			return fmt.Errorf("insert card: %w", err)
		}
		var err error
		c, err = readCard(ctx, q, owner, boardID, id)
		return err
	})
	return c, err
}

// GetCard returns one card on one of the owner's boards.
func (s *Store) GetCard(ctx context.Context, owner, boardID, id uuid.UUID) (Card, error) {
	return readCard(ctx, s.queries(), owner, boardID, id)
}

// readCard reads one card on one of the owner's boards, with its labels.
func readCard(ctx context.Context, q *dbgen.Queries, owner, boardID, id uuid.UUID) (Card, error) {
	row, err := q.GetCard(ctx, dbgen.GetCardParams{ID: id, BoardID: boardID, OwnerID: owner})
	if errors.Is(err, pgx.ErrNoRows) {
		return Card{}, ErrCardNotFound
	}
	if err != nil {
		return Card{}, err
	}
	cards := []Card{cardFromRow(row)}
	if err := attachLabels(ctx, q, boardID, cards); err != nil {
		return Card{}, err
	}
	return cards[0], nil
}

// CardEdit is a partial edit: nil fields (and Due unless SetDue) are left
// as they are.
type CardEdit struct {
	Title       *string
	Description *string
	// SetDue replaces the due with Due; a nil Due clears it.
	SetDue bool
	Due    *Due
	// Archived archives (true) or restores (false) the card. A restored
	// card goes to the bottom of its column.
	Archived *bool
}

// EditCard changes a card's title, markdown description, due, or whether
// it is archived.
func (s *Store) EditCard(ctx context.Context, owner, boardID, id uuid.UUID, e CardEdit) (Card, error) {
	var c Card
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		p := dbgen.UpdateCardParams{
			ID: id, BoardID: boardID, OwnerID: owner, Title: text(e.Title), Description: text(e.Description),
			SetDue: e.SetDue,
		}
		if e.Due != nil {
			if e.Due.At != nil {
				p.DueAt = pgtype.Timestamptz{Time: *e.Due.At, Valid: true}
			} else {
				d, err := time.Parse(dateLayout, e.Due.Date)
				if err != nil {
					return fmt.Errorf("due %q is not a date: %w", e.Due.Date, err)
				}
				p.DueDate = pgtype.Date{Time: d, Valid: true}
			}
		}
		n, err := q.UpdateCard(ctx, p)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrCardNotFound
		}
		switch {
		case e.Archived == nil:
		case *e.Archived:
			err = q.ArchiveCard(ctx, id)
		default:
			err = q.RestoreCard(ctx, id)
		}
		if err != nil {
			return fmt.Errorf("archive or restore card: %w", err)
		}
		c, err = readCard(ctx, q, owner, boardID, id)
		return err
	})
	return c, err
}

func text(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

// MoveCard puts a card into column at index, its slot among the column's
// other cards in priority order (clamped to the column). The card's status
// follows the column. Only the moved card is rewritten, unless repeated
// inserts have exhausted the gap between its new neighbours, in which case
// that column is renumbered first.
func (s *Store) MoveCard(ctx context.Context, owner, boardID, id, column uuid.UUID, index int) (Card, error) {
	var c Card
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		// Column before card, the same order CreateCard takes: a move that
		// renumbers the column then never waits on a card row held by a
		// move that is itself waiting for this column.
		if err := lockColumn(ctx, q, owner, boardID, column); err != nil {
			return err
		}
		locked, err := q.LockOwnedCard(ctx, dbgen.LockOwnedCardParams{ID: id, BoardID: boardID, OwnerID: owner})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCardNotFound
		}
		if err != nil {
			return err
		}
		if locked.ArchivedAt.Valid {
			return ErrCardArchived
		}
		others, err := q.ListColumnPositions(ctx, dbgen.ListColumnPositionsParams{ColumnID: column, ID: id})
		if err != nil {
			return err
		}
		positions := make([]float64, len(others))
		for i, o := range others {
			positions[i] = o.Position
		}
		pos, ok := slotPosition(positions, index)
		if !ok {
			if err := q.RenumberColumn(ctx, dbgen.RenumberColumnParams{ColumnID: column, ID: id}); err != nil {
				return fmt.Errorf("renumber column: %w", err)
			}
			for i := range positions {
				positions[i] = float64(i + 1)
			}
			pos, _ = slotPosition(positions, index)
		}
		if err := q.PlaceCard(ctx, dbgen.PlaceCardParams{ID: id, ColumnID: column, Position: pos}); err != nil {
			return fmt.Errorf("place card: %w", err)
		}
		c, err = readCard(ctx, q, owner, boardID, id)
		return err
	})
	return c, err
}

// ListCards returns the board's cards in priority order, or its archived
// cards (most recently archived first) when archived is true.
func (s *Store) ListCards(ctx context.Context, owner, boardID uuid.UUID, archived bool) ([]Card, error) {
	q := s.queries()
	if err := ownedBoard(ctx, q, owner, boardID); err != nil {
		return nil, err
	}
	var cards []Card
	if archived {
		rows, err := q.ListArchivedCards(ctx, boardID)
		if err != nil {
			return nil, err
		}
		cards = cardsFromRows(rows, func(r dbgen.ListArchivedCardsRow) dbgen.GetCardRow { return dbgen.GetCardRow(r) })
	} else {
		rows, err := q.ListBoardCards(ctx, boardID)
		if err != nil {
			return nil, err
		}
		cards = boardCards(rows)
	}
	return cards, attachLabels(ctx, q, boardID, cards)
}

// boardCards converts ListBoardCards rows.
func boardCards(rows []dbgen.ListBoardCardsRow) []Card {
	return cardsFromRows(rows, func(r dbgen.ListBoardCardsRow) dbgen.GetCardRow { return dbgen.GetCardRow(r) })
}

// cardsFromRows converts any card listing's rows, which share GetCard's
// columns.
func cardsFromRows[R any](rows []R, asCard func(R) dbgen.GetCardRow) []Card {
	out := make([]Card, len(rows))
	for i, r := range rows {
		out[i] = cardFromRow(asCard(r))
	}
	return out
}

// DeleteCard removes a card for good, archived or not.
func (s *Store) DeleteCard(ctx context.Context, owner, boardID, id uuid.UUID) error {
	n, err := s.queries().DeleteCard(ctx, dbgen.DeleteCardParams{ID: id, BoardID: boardID, OwnerID: owner})
	if err == nil && n == 0 {
		return ErrCardNotFound
	}
	return err
}

// slotPosition returns a position that sorts at index among positions
// (ascending, index clamped to 0..len). It reports false when the two
// neighbours are too close for a distinct midpoint, so the caller must
// spread them out and ask again.
func slotPosition(positions []float64, index int) (float64, bool) {
	n := len(positions)
	index = max(0, min(index, n))
	switch {
	case n == 0:
		return 1, true
	case index == 0:
		return positions[0] - 1, true
	case index == n:
		return positions[n-1] + 1, true
	}
	before, after := positions[index-1], positions[index]
	mid := before + (after-before)/2
	return mid, before < mid && mid < after
}
