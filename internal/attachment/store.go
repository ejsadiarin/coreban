package attachment

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/ejsadiarin/coreban/internal/db/sqlc"
)

// Store keeps attachment rows through internal/db/queries/attachments.sql,
// which scopes every statement by owner_id.
type Store struct {
	Pool *pgxpool.Pool
}

// Reserve records an upload the owner is about to make, unless it would
// take them over quota bytes (0: no quota). Every row counts, posted or
// not, so an upload that never becomes a note still uses the quota until
// it is swept.
func (s *Store) Reserve(ctx context.Context, owner, id uuid.UUID, r Request, quota int64) (dbgen.Attachment, error) {
	var row dbgen.Attachment
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		if quota > 0 {
			if err := q.LockAttachmentOwner(ctx, owner); err != nil {
				return err
			}
			used, err := q.AttachmentUsage(ctx, owner)
			if err != nil {
				return err
			}
			if used+r.bytes() > quota {
				return &QuotaError{Used: used, Quota: quota}
			}
		}
		p := dbgen.ReserveAttachmentParams{
			ID: id, OwnerID: owner, Kind: r.Kind, ContentType: r.ContentType, SizeBytes: r.Size,
		}
		if r.Width != nil {
			p.Width = pgtype.Int4{Int32: *r.Width, Valid: true}
		}
		if r.Height != nil {
			p.Height = pgtype.Int4{Int32: *r.Height, Valid: true}
		}
		if r.Poster != nil {
			p.PosterBytes = pgtype.Int8{Int64: r.Poster.Size, Valid: true}
		}
		var err error
		row, err = q.ReserveAttachment(ctx, p)
		return err
	})
	return row, err
}

// Uploaded reports whether a's file, and its poster if it declared one,
// are in the bucket at the sizes it declared.
func (b *Bucket) Uploaded(ctx context.Context, a dbgen.Attachment) (bool, error) {
	files := map[string]int64{Key(a.OwnerID, a.ID): a.SizeBytes}
	if a.PosterBytes.Valid {
		files[PosterKey(a.OwnerID, a.ID)] = a.PosterBytes.Int64
	}
	for key, want := range files {
		size, ok, err := b.Size(ctx, key)
		if err != nil || !ok || size != want {
			return false, err
		}
	}
	return true, nil
}

// Reservation is what the web gets back: the attachment's id and where
// to upload it, and its poster.
type Reservation struct {
	ID           uuid.UUID `json:"id"`
	Upload       Link      `json:"upload"`
	PosterUpload *Link     `json:"poster_upload,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}
