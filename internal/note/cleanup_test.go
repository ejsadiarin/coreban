package note_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/attachment"
	"github.com/ejsadiarin/coreban/internal/testharness"
)

// Keeping the bucket in step with the database (ADR-0004): permanently
// deleting a note queues its files for deletion in the same transaction,
// and a cleaner deletes them, retrying until the bucket confirms; uploads
// never posted are swept after a day.

// clock is a settable time for the cleaner.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// flaky refuses the first n requests' files under prefix (one owner's files: the
// database is shared with other tests), then passes them to the bucket.
type flaky struct {
	attachment.Remover
	prefix string
	mu     sync.Mutex
	n      int
}

func (f *flaky) Remove(ctx context.Context, keys []string) map[string]error {
	f.mu.Lock()
	defer f.mu.Unlock()
	failed := map[string]error{}
	var pass []string
	refused := false
	for _, k := range keys {
		if f.n > 0 && strings.HasPrefix(k, f.prefix) {
			failed[k] = errors.New("bucket unavailable")
			refused = true
			continue
		}
		pass = append(pass, k)
	}
	if refused {
		f.n--
	}
	for k, err := range f.Remover.Remove(ctx, pass) {
		failed[k] = err
	}
	return failed
}

type cleanup struct {
	owner
	id      uuid.UUID
	bucket  *attachment.Bucket
	clock   *clock
	cleaner *attachment.Cleaner
}

// cleanupOwner is an owner whose server and cleaner share the test
// bucket, under quota bytes (0: none). failures is how many removals the
// bucket refuses first.
func cleanupOwner(t *testing.T, quota int64, failures int) cleanup {
	t.Helper()
	id := uuid.New()
	b := testharness.Bucket(t)
	c := &clock{t: time.Now()}
	return cleanup{
		owner:   withBucket(t, testharness.Identity{Owner: id}, quota),
		id:      id,
		bucket:  b,
		clock:   c,
		cleaner: attachment.NewCleaner(testharness.Postgres(t), &flaky{Remover: b, prefix: id.String() + "/", n: failures}, c.now),
	}
}

func (c cleanup) stored(key string) bool {
	c.t.Helper()
	_, ok, err := c.bucket.Size(context.Background(), key)
	if err != nil {
		c.t.Fatalf("stat %s: %v", key, err)
	}
	return ok
}

func (c cleanup) drain() {
	c.t.Helper()
	if err := c.cleaner.Drain(context.Background()); err != nil {
		c.t.Fatalf("drain: %v", err)
	}
}

func (c cleanup) sweep() {
	c.t.Helper()
	if err := c.cleaner.Sweep(context.Background()); err != nil {
		c.t.Fatalf("sweep: %v", err)
	}
}

// uploadVideo reserves and uploads a video of n bytes with a poster.
func (c cleanup) uploadVideo(n int) string {
	c.t.Helper()
	u := c.reserve(map[string]any{"kind": "video", "content_type": "video/mp4", "size": n, "poster": map[string]any{"size": 10}})
	if res := put(c.t, u.Upload, bytes.Repeat([]byte{1}, n)); res.StatusCode != http.StatusOK {
		c.t.Fatalf("upload: status = %d", res.StatusCode)
	}
	if res := put(c.t, *u.PosterUpload, bytes.Repeat([]byte{2}, 10)); res.StatusCode != http.StatusOK {
		c.t.Fatalf("poster upload: status = %d", res.StatusCode)
	}
	return u.ID
}

func (c cleanup) post(ids ...string) string {
	c.t.Helper()
	r := c.postAttached("album", ids...)
	if r.Code != http.StatusCreated {
		c.t.Fatalf("post: status = %d, body %s", r.Code, r.Body)
	}
	return decode[attachedNote](c.t, r).ID
}

func (c cleanup) key(id string) string { return attachment.Key(c.id, uuid.MustParse(id)) }
func (c cleanup) posterKey(id string) string {
	return attachment.PosterKey(c.id, uuid.MustParse(id))
}

// age backdates an upload's reservation by d.
func (c cleanup) age(id string, d time.Duration) {
	c.t.Helper()
	_, err := testharness.Postgres(c.t).Exec(context.Background(),
		`UPDATE attachments SET created_at = created_at - $2::interval WHERE id = $1`, id, d.String())
	if err != nil {
		c.t.Fatalf("backdate: %v", err)
	}
}

func TestPermanentlyDeletingANoteRemovesItsFiles(t *testing.T) {
	c := cleanupOwner(t, 0, 0)
	img, _ := c.uploadImage(100)
	vid := c.uploadVideo(200)
	note := c.post(img, vid)

	if rec := c.do(http.MethodDelete, "/api/coreban/notes/"+note, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, body %s", rec.Code, rec.Body)
	}
	// The files stay while their upload links still work (15 minutes):
	// deleted sooner, they could be uploaded again with nothing to track
	// them.
	c.drain()
	if !c.stored(c.key(img)) {
		t.Fatal("the file went while its upload link still worked")
	}
	c.clock.add(attachment.UploadLinkTTL + time.Minute)
	c.drain()
	for _, key := range []string{c.key(img), c.key(vid), c.posterKey(vid)} {
		if c.stored(key) {
			t.Errorf("%s is still in the bucket", key)
		}
	}
}

func TestAFailedDeleteIsRetriedUntilTheBucketConfirms(t *testing.T) {
	c := cleanupOwner(t, 0, 2)
	img, _ := c.uploadImage(100)
	note := c.post(img)
	c.do(http.MethodDelete, "/api/coreban/notes/"+note, nil)
	c.clock.add(attachment.UploadLinkTTL + time.Minute)

	c.drain() // refused
	if !c.stored(c.key(img)) {
		t.Fatal("the file went though the bucket refused")
	}
	c.drain() // not due again yet: backing off
	c.clock.add(time.Minute)
	c.drain() // refused again
	if !c.stored(c.key(img)) {
		t.Fatal("the file went though the bucket refused twice")
	}
	c.clock.add(2 * time.Minute)
	c.drain()
	if c.stored(c.key(img)) {
		t.Error("the file is still there once the bucket took the delete")
	}
}

func TestArchivingAndRestoringANoteKeepItsFiles(t *testing.T) {
	c := cleanupOwner(t, 0, 0)
	img, _ := c.uploadImage(100)
	note := c.post(img)
	for _, archived := range []bool{true, false} {
		if rec := c.do(http.MethodPatch, "/api/coreban/notes/"+note, map[string]any{"archived": archived}); rec.Code != http.StatusOK {
			t.Fatalf("archived=%v: status = %d, body %s", archived, rec.Code, rec.Body)
		}
	}
	c.sweep()
	c.drain()
	if !c.stored(c.key(img)) {
		t.Error("an archived and restored note lost its file")
	}
	if feed := c.attachedFeed(); len(feed[len(feed)-1].Attachments) != 1 {
		t.Errorf("the note shows %d attachments, want 1", len(feed[len(feed)-1].Attachments))
	}
}

func TestAnUploadNeverPostedIsSweptAfterADay(t *testing.T) {
	c := cleanupOwner(t, 0, 0)
	old, _ := c.uploadImage(100)
	recent, _ := c.uploadImage(101)
	posted, _ := c.uploadImage(102)
	c.post(posted)
	c.age(old, 24*time.Hour+time.Minute)
	c.age(recent, 23*time.Hour)
	c.age(posted, 48*time.Hour)

	c.sweep()
	c.drain()
	if c.stored(c.key(old)) {
		t.Error("an upload unposted for over a day is still in the bucket")
	}
	if !c.stored(c.key(recent)) {
		t.Error("an upload 23 hours old was swept")
	}
	if !c.stored(c.key(posted)) {
		t.Error("a posted attachment was swept")
	}
	// The swept upload can no longer be posted.
	if r := c.postAttached("late", old); r.Code != http.StatusBadRequest {
		t.Errorf("posting a swept upload: status = %d, want 400", r.Code)
	}
	if r := c.postAttached("in time", recent); r.Code != http.StatusCreated {
		t.Errorf("posting a kept upload: status = %d, body %s", r.Code, r.Body)
	}
}

func TestTheQuotaFreesUpOnceSweptFilesAreGone(t *testing.T) {
	c := cleanupOwner(t, 3000, 1)
	abandoned, _ := c.uploadImage(2000)
	if rec := c.do(http.MethodPost, "/api/coreban/attachments", image(2000)); rec.Code != http.StatusConflict {
		t.Fatalf("over quota: status = %d, want 409", rec.Code)
	}
	c.age(abandoned, 25*time.Hour)
	c.sweep()

	// Swept but still in the bucket (its delete was refused): it still
	// counts.
	c.drain()
	if rec := c.do(http.MethodPost, "/api/coreban/attachments", image(2000)); rec.Code != http.StatusConflict {
		t.Fatalf("while its file waits to be deleted: status = %d, want 409", rec.Code)
	}
	c.clock.add(time.Hour)
	c.drain()
	c.reserve(image(2000))
}

func TestARunningCleanerDeletesFilesAsTheirNoteIsDeleted(t *testing.T) {
	id := uuid.New()
	b := testharness.Bucket(t)
	s := testharness.Server(t, testharness.Identity{Owner: id})
	s.UseAttachments(b, 0)
	// Past the upload links' 15 minutes, so the files are due at once.
	later := func() time.Time { return time.Now().Add(attachment.UploadLinkTTL + time.Minute) }
	cleaner := attachment.NewCleaner(testharness.Postgres(t), b, later)
	s.UseCleaner(cleaner)
	c := cleanup{owner: owner{t: t, h: s.Routes()}, id: id, bucket: b}

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { cleaner.Run(ctx); close(done) }()
	t.Cleanup(func() { stop(); <-done })

	img, _ := c.uploadImage(100)
	note := c.post(img)
	c.do(http.MethodDelete, "/api/coreban/notes/"+note, nil)
	deadline := time.Now().Add(5 * time.Second)
	for c.stored(c.key(img)) {
		if time.Now().After(deadline) {
			t.Fatal("the file is still in the bucket 5s after its note was deleted")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
