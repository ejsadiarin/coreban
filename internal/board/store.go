package board

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/ejsadiarin/coreban/internal/db/sqlc"
)

// Store persists boards through the sqlc queries in
// internal/db/queries/boards.sql, which scope every board statement by
// owner_id: an owner can never read or change another owner's board.
type Store struct {
	Pool *pgxpool.Pool
}

func (s *Store) queries() *dbgen.Queries { return dbgen.New(s.Pool) }

// Create makes a board with the default columns in one transaction.
func (s *Store) Create(ctx context.Context, owner uuid.UUID, name string) (Board, error) {
	var b Board
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		b = Board{ID: uuid.New(), Name: name}
		row, err := q.CreateBoard(ctx, dbgen.CreateBoardParams{ID: b.ID, OwnerID: owner, Name: name})
		if err != nil {
			return fmt.Errorf("insert board: %w", err)
		}
		b.CreatedAt, b.UpdatedAt = row.CreatedAt.Time, row.UpdatedAt.Time
		for i, c := range defaultColumns {
			c.ID, c.Position, c.Cards = uuid.New(), int32(i), []Card{}
			if err := q.CreateColumn(ctx, dbgen.CreateColumnParams{
				ID: c.ID, BoardID: b.ID, Name: c.Name, Status: string(c.Status), Position: c.Position,
			}); err != nil {
				return fmt.Errorf("insert column: %w", err)
			}
			b.Columns = append(b.Columns, c)
		}
		return nil
	})
	return b, err
}

// List returns the owner's boards, oldest first, without columns.
func (s *Store) List(ctx context.Context, owner uuid.UUID) ([]Board, error) {
	rows, err := s.queries().ListBoards(ctx, owner)
	if err != nil {
		return nil, err
	}
	boards := make([]Board, 0, len(rows))
	for _, r := range rows {
		boards = append(boards, Board{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time})
	}
	return boards, nil
}

// Get returns one of the owner's boards with its columns in board order and
// each column's cards in priority order, read from one snapshot so a
// concurrent change can't split them.
func (s *Store) Get(ctx context.Context, owner, id uuid.UUID) (Board, error) {
	var b Board
	err := pgx.BeginTxFunc(ctx, s.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			q := dbgen.New(tx)
			row, err := q.GetBoard(ctx, dbgen.GetBoardParams{ID: id, OwnerID: owner})
			if err != nil {
				return notFound(err)
			}
			b = Board{ID: id, Name: row.Name, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
			cols, err := q.ListColumns(ctx, id)
			if err != nil {
				return err
			}
			cards, err := q.ListBoardCards(ctx, id)
			if err != nil {
				return err
			}
			byColumn := map[uuid.UUID][]Card{}
			for _, k := range cards {
				byColumn[k.ColumnID] = append(byColumn[k.ColumnID], cardFromRow(dbgen.GetCardRow(k)))
			}
			b.Columns = make([]Column, 0, len(cols))
			for _, c := range cols {
				colCards := byColumn[c.ID]
				if colCards == nil {
					colCards = []Card{}
				}
				b.Columns = append(b.Columns, Column{
					ID: c.ID, Name: c.Name, Status: Status(c.Status), Position: c.Position, Cards: colCards,
				})
			}
			return nil
		})
	return b, err
}

// Rename changes the name of one of the owner's boards.
func (s *Store) Rename(ctx context.Context, owner, id uuid.UUID, name string) (Board, error) {
	row, err := s.queries().RenameBoard(ctx, dbgen.RenameBoardParams{ID: id, OwnerID: owner, Name: name})
	if err != nil {
		return Board{}, notFound(err)
	}
	return Board{ID: id, Name: name, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}, nil
}

// Delete removes one of the owner's boards and everything on it.
func (s *Store) Delete(ctx context.Context, owner, id uuid.UUID) error {
	n, err := s.queries().DeleteBoard(ctx, dbgen.DeleteBoardParams{ID: id, OwnerID: owner})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// notFound maps "no row for this owner" to ErrNotFound and passes other
// errors through.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
