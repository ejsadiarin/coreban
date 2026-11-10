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

// RefusedError is a column change the board's invariants forbid. Its text
// explains why to the owner.
type RefusedError struct{ reason string }

func (e *RefusedError) Error() string { return e.reason }

// foreignKeyViolation is Postgres's SQLSTATE for a foreign key breach.
const foreignKeyViolation = "23503"

// ColumnEdit is a partial change: nil fields are left as they are.
type ColumnEdit struct {
	Name   *string
	Status *Status
}

// lockBoard confirms the board is the owner's and holds it for the
// transaction, serialising changes to its columns.
func lockBoard(ctx context.Context, q *dbgen.Queries, owner, boardID uuid.UUID) error {
	_, err := q.LockOwnedBoard(ctx, dbgen.LockOwnedBoardParams{ID: boardID, OwnerID: owner})
	return notFound(err)
}

func getColumn(ctx context.Context, q *dbgen.Queries, boardID, id uuid.UUID) (dbgen.GetColumnRow, error) {
	row, err := q.GetColumn(ctx, dbgen.GetColumnParams{ID: id, BoardID: boardID})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrColumnNotFound
	}
	return row, err
}

// columnWithCards reads one column and its cards in priority order.
func columnWithCards(ctx context.Context, q *dbgen.Queries, boardID, id uuid.UUID) (Column, error) {
	row, err := getColumn(ctx, q, boardID, id)
	if err != nil {
		return Column{}, err
	}
	cards, err := q.ListBoardCards(ctx, boardID)
	if err != nil {
		return Column{}, err
	}
	c := Column{ID: row.ID, Name: row.Name, Status: Status(row.Status), Position: row.Position, Cards: []Card{}}
	for _, k := range boardCards(cards) {
		if k.ColumnID == id {
			c.Cards = append(c.Cards, k)
		}
	}
	return c, attachLabels(ctx, q, boardID, c.Cards)
}

// refuseLastOfStatus refuses to take col out of its status when it is the
// board's last column of that status; doing is what the owner was trying
// ("removing it", "changing its status").
func refuseLastOfStatus(ctx context.Context, q *dbgen.Queries, boardID uuid.UUID, col dbgen.GetColumnRow, doing string) error {
	n, err := q.CountStatusColumns(ctx, dbgen.CountStatusColumnsParams{BoardID: boardID, Status: col.Status})
	if err != nil {
		return err
	}
	if n <= 1 {
		label := statusLabels[Status(col.Status)]
		return &RefusedError{fmt.Sprintf("%s is the board's only %s column; add another before %s", col.Name, label, doing)}
	}
	return nil
}

// AddColumn appends a column with a status to one of the owner's boards.
func (s *Store) AddColumn(ctx context.Context, owner, boardID uuid.UUID, name string, status Status) (Column, error) {
	var c Column
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		if err := lockBoard(ctx, q, owner, boardID); err != nil {
			return err
		}
		pos, err := q.NextColumnPosition(ctx, boardID)
		if err != nil {
			return err
		}
		c = Column{ID: uuid.New(), Name: name, Status: status, Position: pos, Cards: []Card{}}
		if err := q.CreateColumn(ctx, dbgen.CreateColumnParams{
			ID: c.ID, BoardID: boardID, Name: name, Status: string(status), Position: pos,
		}); err != nil {
			return fmt.Errorf("insert column: %w", err)
		}
		return nil
	})
	return c, err
}

// EditColumn renames a column or changes its status, never leaving a status
// without a column. Its cards follow the new status (ADR-0001).
func (s *Store) EditColumn(ctx context.Context, owner, boardID, id uuid.UUID, e ColumnEdit) (Column, error) {
	var c Column
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		if err := lockBoard(ctx, q, owner, boardID); err != nil {
			return err
		}
		col, err := getColumn(ctx, q, boardID, id)
		if err != nil {
			return err
		}
		p := dbgen.UpdateColumnParams{ID: id, BoardID: boardID, Name: text(e.Name)}
		if e.Status != nil && string(*e.Status) != col.Status {
			if err := refuseLastOfStatus(ctx, q, boardID, col, "changing its status"); err != nil {
				return err
			}
			status := string(*e.Status)
			p.Status = text(&status)
		}
		if err := q.UpdateColumn(ctx, p); err != nil {
			return fmt.Errorf("update column: %w", err)
		}
		c, err = columnWithCards(ctx, q, boardID, id)
		return err
	})
	return c, err
}

// MoveColumn puts a column at index among the board's columns (clamped to
// either end) and renumbers them in order.
func (s *Store) MoveColumn(ctx context.Context, owner, boardID, id uuid.UUID, index int) (Column, error) {
	var c Column
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		if err := lockBoard(ctx, q, owner, boardID); err != nil {
			return err
		}
		cols, err := q.ListColumns(ctx, boardID)
		if err != nil {
			return err
		}
		order := make([]uuid.UUID, 0, len(cols))
		for _, col := range cols {
			if col.ID != id {
				order = append(order, col.ID)
			}
		}
		if len(order) == len(cols) {
			return ErrColumnNotFound
		}
		index = max(0, min(index, len(order)))
		order = append(order[:index], append([]uuid.UUID{id}, order[index:]...)...)
		// Positions pass through duplicates while they are rewritten.
		if _, err := tx.Exec(ctx, "SET CONSTRAINTS columns_board_id_position_key DEFERRED"); err != nil {
			return err
		}
		for pos, colID := range order {
			if err := q.SetColumnPosition(ctx, dbgen.SetColumnPositionParams{ID: colID, BoardID: boardID, Position: int32(pos)}); err != nil {
				return fmt.Errorf("renumber columns: %w", err)
			}
		}
		c, err = columnWithCards(ctx, q, boardID, id)
		return err
	})
	return c, err
}

// DeleteColumn removes an empty column, unless it is the last of its
// status. A column holding any card, archived or not, is never deleted.
func (s *Store) DeleteColumn(ctx context.Context, owner, boardID, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		if err := lockBoard(ctx, q, owner, boardID); err != nil {
			return err
		}
		col, err := getColumn(ctx, q, boardID, id)
		if err != nil {
			return err
		}
		holdsCards := &RefusedError{fmt.Sprintf("%s still holds cards, archived ones included; move or delete them first (restore an archived card to move it)", col.Name)}
		n, err := q.CountColumnCards(ctx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return holdsCards
		}
		if err := refuseLastOfStatus(ctx, q, boardID, col, "removing it"); err != nil {
			return err
		}
		// A card placed since the count is caught by the foreign key.
		err = q.DeleteColumn(ctx, dbgen.DeleteColumnParams{ID: id, BoardID: boardID})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return holdsCards
		}
		return err
	})
}
