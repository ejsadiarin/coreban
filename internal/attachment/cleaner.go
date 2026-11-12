package attachment

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"

	dbgen "github.com/ejsadiarin/coreban/internal/db/sqlc"
)

// Remover deletes files from the bucket and says which it couldn't, with
// why. Deleting a file that isn't there succeeds: it is gone either way.
type Remover interface {
	Remove(ctx context.Context, keys []string) (failed map[string]error)
}

// Remove deletes keys in one multi-object request.
func (b *Bucket) Remove(ctx context.Context, keys []string) map[string]error {
	objects := make(chan minio.ObjectInfo, len(keys))
	for _, k := range keys {
		objects <- minio.ObjectInfo{Key: k}
	}
	close(objects)
	failed := map[string]error{}
	for e := range b.client.RemoveObjects(ctx, b.name, objects, minio.RemoveObjectsOptions{}) {
		failed[e.ObjectName] = e.Err
	}
	return failed
}

// How the cleaner paces itself. An upload no note took within UploadTTL
// is swept, every sweepEvery. A refused delete is retried after
// retryBase, doubling each time up to retryMax. A claimed file is leased
// for lease, after which another cleaner may take it.
const (
	UploadTTL  = 24 * time.Hour
	sweepEvery = time.Hour
	retryBase  = 30 * time.Second
	retryMax   = time.Hour
	lease      = 5 * time.Minute
	claimBatch = 100
)

// Cleaner keeps the bucket in step with the database (ADR-0004). It
// deletes the files queued when attachment rows go, retrying with backoff
// until the bucket confirms, and sweeps uploads no note took. Several may
// run at once: each queued file is leased to one of them at a time.
type Cleaner struct {
	pool  *pgxpool.Pool
	files Remover
	now   func() time.Time
	wake  chan struct{}
}

// NewCleaner returns a cleaner deleting files through files and reading
// the time from now.
func NewCleaner(pool *pgxpool.Pool, files Remover, now func() time.Time) *Cleaner {
	return &Cleaner{pool: pool, files: files, now: now, wake: make(chan struct{}, 1)}
}

// Wake makes Run delete queued files now, as a note is deleted.
func (c *Cleaner) Wake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Sweep deletes uploads no note took within UploadTTL; their files are
// queued for deletion with them.
func (c *Cleaner) Sweep(ctx context.Context) error {
	n, err := dbgen.New(c.pool).SweepUploads(ctx, ts(c.now().Add(-UploadTTL)))
	if n > 0 {
		slog.Info("attachments: swept abandoned uploads", "count", n)
	}
	return err
}

// Drain deletes every queued file that is due, a batch at a time, until
// none is. A file the bucket refuses waits for its next attempt.
func (c *Cleaner) Drain(ctx context.Context) error {
	for {
		n, err := c.batch(ctx)
		if err != nil || n < claimBatch {
			return err
		}
	}
}

// batch leases up to claimBatch due files, deletes them from the bucket
// outside any transaction, then records what happened to each.
func (c *Cleaner) batch(ctx context.Context) (int, error) {
	q := dbgen.New(c.pool)
	now := c.now()
	due, err := q.ClaimDeletions(ctx, dbgen.ClaimDeletionsParams{
		Now: ts(now), LeaseUntil: ts(now.Add(lease)), MaxRows: claimBatch,
	})
	if err != nil || len(due) == 0 {
		return 0, err
	}
	keys := make([]string, len(due))
	for i, d := range due {
		keys[i] = d.Key
	}
	failed := c.files.Remove(ctx, keys)
	if ctx.Err() != nil {
		return 0, ctx.Err() // leased: retried once the lease ends
	}
	done := make([]string, 0, len(due))
	failedAt := c.now()
	for _, d := range due {
		err, refused := failed[d.Key]
		if !refused {
			done = append(done, d.Key)
			continue
		}
		slog.Warn("attachments: delete refused; will retry", "key", d.Key, "attempt", d.Attempts+1, "err", err)
		if err := q.DeletionFailed(ctx, dbgen.DeletionFailedParams{
			Key:           d.Key,
			NextAttemptAt: ts(failedAt.Add(backoff(d.Attempts + 1))),
			LastError:     pgtype.Text{String: errText(err), Valid: true},
		}); err != nil {
			return 0, err
		}
	}
	return len(due), q.DeletionsDone(ctx, done)
}

func errText(err error) string {
	if err == nil {
		return "refused"
	}
	return err.Error()
}

// backoff is how long the attempt-th refused delete waits before the next.
func backoff(attempt int32) time.Duration {
	d := retryBase
	for i := int32(1); i < attempt && d < retryMax; i++ {
		d *= 2
	}
	return min(d, retryMax)
}

// Run sweeps and drains until ctx ends: at start, whenever woken, when
// the next queued file is due, and sweeping every sweepEvery.
func (c *Cleaner) Run(ctx context.Context) {
	nextSweep := c.now()
	for {
		if !c.now().Before(nextSweep) {
			nextSweep = c.now().Add(sweepEvery)
			if err := c.Sweep(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Error("attachments: sweep failed; will retry", "err", err)
				nextSweep = c.now().Add(retryBase)
			}
		}
		wake := nextSweep
		if err := c.Drain(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("attachments: deleting queued files failed; will retry", "err", err)
			wake = earliest(wake, c.now().Add(retryBase))
		} else if next, err := dbgen.New(c.pool).NextDeletion(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("attachments: finding the next queued file failed; will retry", "err", err)
			wake = earliest(wake, c.now().Add(retryBase))
		} else if next.Valid {
			wake = earliest(wake, next.Time)
		}
		t := time.NewTimer(max(wake.Sub(c.now()), time.Second))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-c.wake:
			t.Stop()
		case <-t.C:
		}
	}
}

func earliest(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
