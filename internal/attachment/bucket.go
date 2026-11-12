package attachment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// BucketConfig is where attachments are stored: any S3-compatible bucket
// (Cloudflare R2 in production, ADR-0004).
type BucketConfig struct {
	// Endpoint is how coreban reaches the bucket, e.g.
	// https://<account>.r2.cloudflarestorage.com.
	Endpoint string
	// PublicEndpoint is how phones reach it, which signed links name; it
	// defaults to Endpoint. They differ when coreban sees the store on an
	// internal address.
	PublicEndpoint string
	Bucket         string
	AccessKeyID    string
	SecretKey      string
	// Region must match the store's ("auto" for R2): signatures cover it.
	Region string
}

// Bucket stores attachment files and signs the links phones use to upload
// and read them. Signing is local: it never calls the store.
type Bucket struct {
	name   string
	client *minio.Client // coreban's own calls
	signer *minio.Client // links for phones, on PublicEndpoint

	mu    sync.Mutex
	reads map[string]readLink // key → its current read link
}

type readLink struct {
	url     string
	expires time.Time
}

// maxCachedLinks bounds the read-link cache; past it the cache starts over.
const maxCachedLinks = 50_000

// How long a signed link works. An upload link only needs to outlast the
// upload; a read link is made fresh each time the Feed is shown.
const (
	UploadLinkTTL = 15 * time.Minute
	ReadLinkTTL   = time.Hour
)

// NewBucket checks cfg and builds a Bucket on it.
func NewBucket(cfg BucketConfig) (*Bucket, error) {
	if cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretKey == "" || cfg.Region == "" {
		return nil, errors.New("attachment bucket: bucket, access key, secret and region are required")
	}
	client, err := newClient(cfg.Endpoint, cfg)
	if err != nil {
		return nil, fmt.Errorf("attachment bucket endpoint: %w", err)
	}
	public := cfg.PublicEndpoint
	if public == "" {
		public = cfg.Endpoint
	}
	signer, err := newClient(public, cfg)
	if err != nil {
		return nil, fmt.Errorf("attachment bucket public endpoint: %w", err)
	}
	return &Bucket{name: cfg.Bucket, client: client, signer: signer, reads: map[string]readLink{}}, nil
}

func newClient(endpoint string, cfg BucketConfig) (*minio.Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("%q must be an http(s) URL with no path", endpoint)
	}
	// A set region keeps minio-go from asking the store for it, so
	// signing stays local.
	return minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretKey, ""),
		Secure: u.Scheme == "https",
		Region: cfg.Region,
	})
}

// Key is where an attachment's file lives; PosterKey its video's poster.
func Key(owner, id uuid.UUID) string       { return owner.String() + "/" + id.String() }
func PosterKey(owner, id uuid.UUID) string { return Key(owner, id) + ".poster" }

// Link is a signed request a phone makes itself: the URL, its method, and
// the headers it must send with exactly these values.
type Link struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
}

// UploadLink signs a PUT of exactly size bytes of contentType to key. Both
// are part of the signature, so the store refuses any other file.
func (b *Bucket) UploadLink(ctx context.Context, key, contentType string, size int64) (Link, error) {
	h := http.Header{}
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	u, err := b.signer.PresignHeader(ctx, http.MethodPut, b.name, key, UploadLinkTTL, nil, h)
	if err != nil {
		return Link{}, err
	}
	// A browser sets Content-Length from the body itself.
	return Link{URL: u.String(), Method: http.MethodPut, Headers: map[string]string{"Content-Type": contentType}}, nil
}

// ReadLink signs a GET of key. A link with more than half its life left
// is handed out again, so the same file keeps the same URL from one Feed
// load to the next and the phone's cache can serve it.
func (b *Bucket) ReadLink(ctx context.Context, key string) (string, error) {
	now := time.Now()
	b.mu.Lock()
	cached, ok := b.reads[key]
	b.mu.Unlock()
	if ok && cached.expires.Sub(now) > ReadLinkTTL/2 {
		return cached.url, nil
	}
	u, err := b.signer.PresignedGetObject(ctx, b.name, key, ReadLinkTTL, nil)
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	if len(b.reads) >= maxCachedLinks {
		b.reads = map[string]readLink{}
	}
	b.reads[key] = readLink{url: u.String(), expires: now.Add(ReadLinkTTL)}
	b.mu.Unlock()
	return u.String(), nil
}

// Size is the stored size of key, or ok false when nothing is there.
func (b *Bucket) Size(ctx context.Context, key string) (size int64, ok bool, err error) {
	info, err := b.client.StatObject(ctx, b.name, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).StatusCode == http.StatusNotFound {
			return 0, false, nil
		}
		return 0, false, err
	}
	return info.Size, true, nil
}
