// Package attachment keeps the images and videos posted with plain notes
// (ADR-0004). Their bytes live in an S3-compatible bucket; coreban keeps a
// row per attachment, signs the links phones upload and read them through,
// and holds each owner under a quota.
package attachment

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	dbgen "github.com/ejsadiarin/coreban/internal/db/sqlc"
)

// Kinds of attachment, and what each may be. Images arrive already
// re-encoded to WebP on the phone, or as an animated GIF kept as it is;
// videos as they were recorded.
const (
	KindImage = "image"
	KindVideo = "video"
)

var contentTypes = map[string][]string{
	KindImage: {"image/webp", "image/gif"},
	KindVideo: {"video/mp4", "video/webm", "video/quicktime"},
}

// Size limits, in bytes, and how many attachments one note holds.
const (
	MaxImageBytes  = 5 << 20
	MaxVideoBytes  = 25 << 20
	MaxPosterBytes = 1 << 20
	MaxPerNote     = 10
)

// ErrNoBucket is returned when attachments are used on a server with no
// bucket configured.
var ErrNoBucket = errors.New("attachments aren't set up on this server")

// DefaultQuota is how much one owner may store unless ATTACHMENT_QUOTA
// says otherwise. Admins have none.
const DefaultQuota = 5 << 30

// PosterContentType is a video's poster frame, taken by the phone.
const PosterContentType = "image/webp"

// Request describes a file the web is about to upload.
type Request struct {
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Width       *int32 `json:"width"`
	Height      *int32 `json:"height"`
	// Poster is a video's poster frame, uploaded beside it.
	Poster *struct {
		Size int64 `json:"size"`
	} `json:"poster"`
}

// Refusal is a file the Feed doesn't take, and why.
type Refusal struct {
	// TooLarge separates a file over its size limit from one that is
	// not allowed at all.
	TooLarge bool
	Reason   string
}

func (r *Refusal) Error() string { return r.Reason }

// Check refuses a request for a file the Feed doesn't take.
func (r Request) Check() *Refusal {
	allowed, ok := contentTypes[r.Kind]
	switch {
	case !ok:
		return &Refusal{Reason: `kind must be "image" or "video"`}
	case !slices.Contains(allowed, r.ContentType):
		return &Refusal{Reason: fmt.Sprintf("a %s must be one of %s", r.Kind, strings.Join(allowed, ", "))}
	case r.Size <= 0:
		return &Refusal{Reason: "size must be the file's size in bytes"}
	case r.Width != nil && *r.Width <= 0, r.Height != nil && *r.Height <= 0:
		return &Refusal{Reason: "width and height must be positive"}
	case r.Kind == KindImage && r.Size > MaxImageBytes:
		return &Refusal{TooLarge: true, Reason: "an image may be at most 5 MB"}
	case r.Kind == KindVideo && r.Size > MaxVideoBytes:
		return &Refusal{TooLarge: true, Reason: "a video may be at most 25 MB"}
	}
	if r.Poster != nil {
		switch {
		case r.Kind != KindVideo:
			return &Refusal{Reason: "only a video has a poster"}
		case r.Poster.Size <= 0:
			return &Refusal{Reason: "poster size must be its size in bytes"}
		case r.Poster.Size > MaxPosterBytes:
			return &Refusal{TooLarge: true, Reason: "a poster may be at most 1 MB"}
		}
	}
	return nil
}

// bytes is what the request counts toward the quota.
func (r Request) bytes() int64 {
	n := r.Size
	if r.Poster != nil {
		n += r.Poster.Size
	}
	return n
}

// QuotaError is a reservation that would take the owner over their quota.
type QuotaError struct {
	Used, Quota int64
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("storage quota reached: %s of %s used (%d of %d bytes)",
		human(e.Used), human(e.Quota), e.Used, e.Quota)
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
}

// View is an attachment as a note shows it. The links are signed afresh
// for each read and absent on an admin's targeted read of another owner.
// Filename is what the web saves it as on Download: it fetches URL itself,
// which works the same on every S3-compatible store.
type View struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Width       *int32 `json:"width,omitempty"`
	Height      *int32 `json:"height,omitempty"`
	Filename    string `json:"filename"`
	URL         string `json:"url,omitempty"`
	PosterURL   string `json:"poster_url,omitempty"`
}

var extensions = map[string]string{
	"image/webp": "webp", "image/gif": "gif",
	"video/mp4": "mp4", "video/webm": "webm", "video/quicktime": "mov",
}

// Render shows rows as their notes do, signing links on b when links is
// set (and b is configured).
func Render(ctx context.Context, b *Bucket, rows []dbgen.Attachment, links bool) ([]View, error) {
	out := make([]View, len(rows))
	for i, a := range rows {
		v := View{
			ID: a.ID.String(), Kind: a.Kind, ContentType: a.ContentType, Size: a.SizeBytes,
			Filename: fmt.Sprintf("coreban-%s-%s.%s",
				a.CreatedAt.Time.UTC().Format("20060102"), a.ID.String()[:8], extensions[a.ContentType]),
		}
		if a.Width.Valid {
			v.Width = &a.Width.Int32
		}
		if a.Height.Valid {
			v.Height = &a.Height.Int32
		}
		if links && b != nil {
			var err error
			if v.URL, err = b.ReadLink(ctx, Key(a.OwnerID, a.ID)); err != nil {
				return nil, err
			}
			if a.PosterBytes.Valid {
				if v.PosterURL, err = b.ReadLink(ctx, PosterKey(a.OwnerID, a.ID)); err != nil {
					return nil, err
				}
			}
		}
		out[i] = v
	}
	return out, nil
}
