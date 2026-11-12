package note_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ejsadiarin/coreban/internal/testharness"
)

// Attachments (ADR-0004): the web asks coreban for an upload link per
// file, PUTs the file straight to the bucket, then posts the note naming
// the uploads. These tests play the web against a local S3.

type uploadLink struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
}

type upload struct {
	ID           string      `json:"id"`
	Upload       uploadLink  `json:"upload"`
	PosterUpload *uploadLink `json:"poster_upload"`
}

type attachmentView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	URL         string `json:"url"`
	Filename    string `json:"filename"`
	PosterURL   string `json:"poster_url"`
}

type attachedNote struct {
	ID          string           `json:"id"`
	Text        string           `json:"text"`
	Attachments []attachmentView `json:"attachments"`
}

// withBucket is an owner on a server whose attachments live in the test
// bucket, under quota bytes (0: none).
func withBucket(t *testing.T, id testharness.Identity, quota int64) owner {
	t.Helper()
	s := testharness.Server(t, id)
	s.UseAttachments(testharness.Bucket(t), quota)
	return owner{t: t, h: s.Routes()}
}

func bucketOwner(t *testing.T, quota int64) (owner, uuid.UUID) {
	id := uuid.New()
	return withBucket(t, testharness.Identity{Owner: id}, quota), id
}

func (o owner) reserve(body map[string]any) upload {
	o.t.Helper()
	rec := o.do(http.MethodPost, "/api/coreban/attachments", body)
	if rec.Code != http.StatusCreated {
		o.t.Fatalf("reserve %v: status = %d, body %s", body, rec.Code, rec.Body)
	}
	return decode[upload](o.t, rec)
}

func image(size int) map[string]any {
	return map[string]any{"kind": "image", "content_type": "image/webp", "size": size, "width": 640, "height": 480}
}

// put sends data to a signed upload link the way a browser does.
func put(t *testing.T, l uploadLink, data []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(l.Method, l.URL, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	for k, v := range l.Headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	res.Body.Close()
	return res
}

// uploadImage reserves and uploads one image of n bytes.
func (o owner) uploadImage(n int) (string, []byte) {
	o.t.Helper()
	data := bytes.Repeat([]byte{byte(n)}, n)
	u := o.reserve(image(n))
	if res := put(o.t, u.Upload, data); res.StatusCode != http.StatusOK {
		o.t.Fatalf("upload: status = %d", res.StatusCode)
	}
	return u.ID, data
}

func (o owner) postAttached(text string, ids ...string) *httptest.ResponseRecorder {
	o.t.Helper()
	body := map[string]any{"attachments": ids}
	if text != "" {
		body["text"] = text
	}
	return o.do(http.MethodPost, "/api/coreban/notes", body)
}

func get(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func (o owner) attachedFeed() []attachedNote {
	o.t.Helper()
	rec := o.do(http.MethodGet, "/api/coreban/notes", nil)
	if rec.Code != http.StatusOK {
		o.t.Fatalf("feed: status = %d, body %s", rec.Code, rec.Body)
	}
	return decode[struct {
		Notes []attachedNote `json:"notes"`
	}](o.t, rec).Notes
}

func TestAnUploadLinkIsRefusedForAFileTheFeedDoesNotTake(t *testing.T) {
	o, _ := bucketOwner(t, 0)
	const mb = 1 << 20
	for name, c := range map[string]struct {
		body map[string]any
		want int
	}{
		"an image not yet compressed": {map[string]any{"kind": "image", "content_type": "image/jpeg", "size": 1000}, http.StatusBadRequest},
		"an image over 5 MB":          {map[string]any{"kind": "image", "content_type": "image/webp", "size": 5*mb + 1}, http.StatusRequestEntityTooLarge},
		"a video over 25 MB":          {map[string]any{"kind": "video", "content_type": "video/mp4", "size": 25*mb + 1}, http.StatusRequestEntityTooLarge},
		"a poster over 1 MB":          {map[string]any{"kind": "video", "content_type": "video/mp4", "size": 100, "poster": map[string]any{"size": mb + 1}}, http.StatusRequestEntityTooLarge},
		"a poster on an image":        {map[string]any{"kind": "image", "content_type": "image/webp", "size": 100, "poster": map[string]any{"size": 10}}, http.StatusBadRequest},
		"an audio file":               {map[string]any{"kind": "audio", "content_type": "audio/mpeg", "size": 100}, http.StatusBadRequest},
		"a video in an image's type":  {map[string]any{"kind": "video", "content_type": "image/webp", "size": 100}, http.StatusBadRequest},
		"an empty file":               {map[string]any{"kind": "image", "content_type": "image/webp", "size": 0}, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			if rec := o.do(http.MethodPost, "/api/coreban/attachments", c.body); rec.Code != c.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, c.want, rec.Body)
			}
		})
	}
	// The largest of each is fine.
	o.reserve(image(5 * mb))
	o.reserve(map[string]any{"kind": "image", "content_type": "image/gif", "size": 5 * mb})
	o.reserve(map[string]any{"kind": "video", "content_type": "video/quicktime", "size": 25 * mb, "poster": map[string]any{"size": mb}})
}

func TestADemoGuestCantUpload(t *testing.T) {
	guest := withBucket(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"guest"}}, 0)
	if rec := guest.do(http.MethodPost, "/api/coreban/attachments", image(100)); rec.Code != http.StatusForbidden {
		t.Errorf("guest upload: status = %d, want 403 (body %s)", rec.Code, rec.Body)
	}
}

func TestUploadsWaitWithoutABucket(t *testing.T) {
	o, _ := newOwner(t) // a server with no bucket configured
	if rec := o.do(http.MethodPost, "/api/coreban/attachments", image(100)); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (body %s)", rec.Code, rec.Body)
	}
}

func TestAPostedImageIsShownAndDownloadedThroughSignedLinks(t *testing.T) {
	o, _ := bucketOwner(t, 0)
	id, data := o.uploadImage(2048)

	r := o.postAttached("sunset", id)
	if r.Code != http.StatusCreated {
		t.Fatalf("post: status = %d, body %s", r.Code, r.Body)
	}
	feed := o.attachedFeed()
	n := feed[len(feed)-1]
	if n.Text != "sunset" || len(n.Attachments) != 1 {
		t.Fatalf("note = %+v, want the caption and one attachment", n)
	}
	a := n.Attachments[0]
	if a.ID != id || a.Kind != "image" || a.ContentType != "image/webp" || a.Size != 2048 || a.Width != 640 || a.Height != 480 {
		t.Errorf("attachment = %+v", a)
	}
	res, got := get(t, a.URL)
	if res.StatusCode != http.StatusOK || !bytes.Equal(got, data) {
		t.Errorf("view link: status %d, %d bytes, want the uploaded file", res.StatusCode, len(got))
	}
	// The web saves it under this name (Download).
	if want := "coreban-" + time.Now().UTC().Format("20060102") + "-" + id[:8] + ".webp"; a.Filename != want {
		t.Errorf("filename = %q, want %q", a.Filename, want)
	}
}

func TestTheBucketTakesOnlyTheFileTheLinkWasMadeFor(t *testing.T) {
	o, _ := bucketOwner(t, 0)
	u := o.reserve(image(100))
	if res := put(t, u.Upload, bytes.Repeat([]byte{1}, 5000)); res.StatusCode == http.StatusOK {
		t.Errorf("a bigger file than declared uploaded (status %d)", res.StatusCode)
	}
	l := u.Upload
	l.Headers = map[string]string{"Content-Type": "text/html"}
	if res := put(t, l, bytes.Repeat([]byte{1}, 100)); res.StatusCode == http.StatusOK {
		t.Errorf("another content type uploaded (status %d)", res.StatusCode)
	}
}

func TestAVideoKeepsItsPoster(t *testing.T) {
	o, _ := bucketOwner(t, 0)
	video, poster := bytes.Repeat([]byte{7}, 3000), bytes.Repeat([]byte{8}, 300)
	u := o.reserve(map[string]any{"kind": "video", "content_type": "video/mp4", "size": len(video), "poster": map[string]any{"size": len(poster)}})
	if u.PosterUpload == nil {
		t.Fatal("no poster upload link for a video with a poster")
	}
	put(t, u.Upload, video)

	// The poster it declared hasn't arrived: not posted yet.
	if r := o.postAttached("", u.ID); r.Code != http.StatusConflict {
		t.Fatalf("post without its poster: status = %d, want 409 (body %s)", r.Code, r.Body)
	}
	put(t, *u.PosterUpload, poster)
	if r := o.postAttached("", u.ID); r.Code != http.StatusCreated {
		t.Fatalf("post: status = %d, body %s", r.Code, r.Body)
	}
	feed := o.attachedFeed()
	a := feed[len(feed)-1].Attachments[0]
	if _, got := get(t, a.PosterURL); !bytes.Equal(got, poster) {
		t.Errorf("poster link serves %d bytes, want the poster", len(got))
	}
}

func TestANoteHoldsUpToTenAttachmentsInOrder(t *testing.T) {
	o, _ := bucketOwner(t, 0)
	var ids []string
	for i := range 11 {
		id, _ := o.uploadImage(100 + i)
		ids = append(ids, id)
	}
	if r := o.postAttached("", ids...); r.Code != http.StatusBadRequest {
		t.Errorf("11 attachments: status = %d, want 400", r.Code)
	}
	if r := o.postAttached("", ids[:10]...); r.Code != http.StatusCreated {
		t.Fatalf("10 attachments: status = %d, body %s", r.Code, r.Body)
	}
	feed := o.attachedFeed()
	n := feed[len(feed)-1]
	if n.Text != "" || len(n.Attachments) != 10 {
		t.Fatalf("note = %q with %d attachments, want none and 10", n.Text, len(n.Attachments))
	}
	for i, a := range n.Attachments {
		if a.ID != ids[i] {
			t.Errorf("attachment %d = %s, want %s: the order they were posted in", i, a.ID, ids[i])
		}
	}
}

func TestANoteNamesOnlyYourOwnFinishedUploadsOnce(t *testing.T) {
	o, _ := bucketOwner(t, 0)
	other, _ := bucketOwner(t, 0)
	theirs, _ := other.uploadImage(100)
	mine, _ := o.uploadImage(100)
	reserved := o.reserve(image(100)) // never uploaded

	for name, c := range map[string]struct {
		body map[string]any
		want int
	}{
		"someone else's upload": {map[string]any{"attachments": []string{theirs}}, http.StatusBadRequest},
		"an unknown id":         {map[string]any{"attachments": []string{uuid.NewString()}}, http.StatusBadRequest},
		"a malformed id":        {map[string]any{"attachments": []string{"photo.webp"}}, http.StatusBadRequest},
		"the same one twice":    {map[string]any{"attachments": []string{mine, mine}}, http.StatusBadRequest},
		"an unfinished upload":  {map[string]any{"attachments": []string{reserved.ID}}, http.StatusConflict},
		"a link as well":        {map[string]any{"attachments": []string{mine}, "link": map[string]string{"kind": "expense", "id": uuid.NewString()}}, http.StatusBadRequest},
		"none, and no text":     {map[string]any{"attachments": []string{}}, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			if rec := o.do(http.MethodPost, "/api/coreban/notes", c.body); rec.Code != c.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, c.want, rec.Body)
			}
		})
	}
	if got := len(o.attachedFeed()); got != 0 {
		t.Fatalf("feed has %d notes after the refusals, want none", got)
	}

	if r := o.postAttached("", mine); r.Code != http.StatusCreated {
		t.Fatalf("post: status = %d, body %s", r.Code, r.Body)
	}
	if r := o.postAttached("again", mine); r.Code != http.StatusBadRequest {
		t.Errorf("posting an attachment twice: status = %d, want 400", r.Code)
	}
}

func TestTheQuotaCountsEveryUploadAndSparesAdmins(t *testing.T) {
	o, _ := bucketOwner(t, 1000)
	o.uploadImage(600) // posted below
	o.reserve(image(300))
	rec := o.do(http.MethodPost, "/api/coreban/attachments", image(200))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "900") {
		t.Errorf("over quota: status = %d, body %s; want 409 saying what's used", rec.Code, rec.Body)
	}
	o.reserve(image(100)) // exactly at the quota

	admin := withBucket(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"admin"}}, 1000)
	admin.reserve(image(5 << 20))
	admin.reserve(image(5 << 20))
}

func TestAnAdminsTargetedReadShowsAttachmentsWithoutLinks(t *testing.T) {
	o, ownerID := bucketOwner(t, 0)
	id, _ := o.uploadImage(100)
	o.postAttached("private", id)

	admin := withBucket(t, testharness.Identity{Owner: uuid.New(), Scopes: []string{"admin"}, ActAs: ownerID}, 0)
	feed := admin.attachedFeed()
	if len(feed) != 1 || len(feed[0].Attachments) != 1 {
		t.Fatalf("targeted feed = %+v, want the owner's note and its attachment", feed)
	}
	if a := feed[0].Attachments[0]; a.Size != 100 || a.URL != "" || a.PosterURL != "" {
		t.Errorf("targeted read shows %+v, want its size and no links", a)
	}
}

func TestACaptionCanBeClearedButANoteNeverEmptied(t *testing.T) {
	o, _ := bucketOwner(t, 0)
	id, _ := o.uploadImage(100)
	n := decode[attachedNote](t, o.postAttached("caption", id))

	rec := o.patch(n.ID, map[string]string{"text": "  "})
	if got := decode[attachedNote](t, rec); rec.Code != http.StatusOK || got.Text != "" || len(got.Attachments) != 1 {
		t.Errorf("clearing the caption: status %d, note %+v", rec.Code, got)
	}
	plain := o.post("just text")
	if rec := o.patch(plain.ID, map[string]string{"text": ""}); rec.Code != http.StatusBadRequest {
		t.Errorf("emptying a plain note: status = %d, want 400", rec.Code)
	}
}

func TestANoteWithAttachmentsStaysAPlainNote(t *testing.T) {
	o, _ := bucketOwner(t, 0)
	id, _ := o.uploadImage(100)
	n := decode[attachedNote](t, o.postAttached("fix the NAS", id))
	b := o.newBoard("Homelab")
	rec := o.do(http.MethodPost, "/api/coreban/boards/"+b.ID+"/cards",
		map[string]any{"column_id": b.Columns[0].ID, "title": "fix the NAS", "note": n.ID})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("making a card of it: status = %d, want 400 (body %s)", rec.Code, rec.Body)
	}
	if feed := o.attachedFeed(); len(feed) != 1 || len(feed[0].Attachments) != 1 {
		t.Errorf("feed = %+v, want the note unchanged", feed)
	}
}

// The phone caches what it downloads, so a file's link stays the same from
// one Feed load to the next while it has long enough left to work.
func TestAFilesLinkStaysTheSameBetweenFeedLoads(t *testing.T) {
	o, _ := bucketOwner(t, 0)
	id, _ := o.uploadImage(100)
	o.postAttached("", id)
	first := o.attachedFeed()[0].Attachments[0].URL
	time.Sleep(1100 * time.Millisecond) // signatures carry the time to the second
	if again := o.attachedFeed()[0].Attachments[0].URL; again != first {
		t.Errorf("link changed between loads:\n%s\n%s", first, again)
	}
}
