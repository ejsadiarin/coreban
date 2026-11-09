package board

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists boards. Every statement is scoped by owner_id, so an
// owner can never read or change another owner's board.
type Store struct {
	Pool *pgxpool.Pool
}

// Create makes a board with the default columns in one transaction.
func (s *Store) Create(ctx context.Context, owner uuid.UUID, name string) (Board, error) {
	var b Board
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		b = Board{ID: uuid.New(), Name: name}
		if err := tx.QueryRow(ctx,
			`INSERT INTO boards (id, owner_id, name) VALUES ($1, $2, $3)
			 RETURNING created_at, updated_at`,
			b.ID, owner, name).Scan(&b.CreatedAt, &b.UpdatedAt); err != nil {
			return fmt.Errorf("insert board: %w", err)
		}
		for i, c := range defaultColumns {
			c.ID, c.Position = uuid.New(), int32(i)
			if _, err := tx.Exec(ctx,
				`INSERT INTO columns (id, board_id, name, status, position) VALUES ($1, $2, $3, $4, $5)`,
				c.ID, b.ID, c.Name, c.Status, c.Position); err != nil {
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
	rows, err := s.Pool.Query(ctx,
		`SELECT id, name, created_at, updated_at FROM boards
		 WHERE owner_id = $1 ORDER BY created_at, id`, owner)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Board, error) {
		var b Board
		err := row.Scan(&b.ID, &b.Name, &b.CreatedAt, &b.UpdatedAt)
		return b, err
	})
}

// Get returns one of the owner's boards with its columns in board order,
// read from one snapshot so a concurrent change can't split the two.
func (s *Store) Get(ctx context.Context, owner, id uuid.UUID) (Board, error) {
	var b Board
	err := pgx.BeginTxFunc(ctx, s.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			var err error
			b, err = get(ctx, tx, owner, id)
			return err
		})
	return b, err
}

func get(ctx context.Context, tx pgx.Tx, owner, id uuid.UUID) (Board, error) {
	b := Board{ID: id}
	err := tx.QueryRow(ctx,
		`SELECT name, created_at, updated_at FROM boards WHERE id = $1 AND owner_id = $2`,
		id, owner).Scan(&b.Name, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Board{}, ErrNotFound
	}
	if err != nil {
		return Board{}, err
	}
	rows, err := tx.Query(ctx,
		`SELECT id, name, status, position FROM columns WHERE board_id = $1 ORDER BY position`, id)
	if err != nil {
		return Board{}, err
	}
	b.Columns, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Column, error) {
		var c Column
		err := row.Scan(&c.ID, &c.Name, &c.Status, &c.Position)
		return c, err
	})
	return b, err
}

// Rename changes the name of one of the owner's boards.
func (s *Store) Rename(ctx context.Context, owner, id uuid.UUID, name string) (Board, error) {
	b := Board{ID: id, Name: name}
	err := s.Pool.QueryRow(ctx,
		`UPDATE boards SET name = $3, updated_at = now() WHERE id = $1 AND owner_id = $2
		 RETURNING created_at, updated_at`,
		id, owner, name).Scan(&b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Board{}, ErrNotFound
	}
	return b, err
}

// Delete removes one of the owner's boards and everything on it.
func (s *Store) Delete(ctx context.Context, owner, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM boards WHERE id = $1 AND owner_id = $2`, id, owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
