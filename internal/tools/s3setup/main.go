// Command s3setup prepares a local S3 for coreban's attachments, as an
// operator prepares R2 by hand (README "Attachments"): it creates the
// bucket and lets the dashboard's origins PUT and GET in it directly. Only
// the e2e stack (coregateway scripts/e2e-up.sh) runs it.
//
//	go run ./internal/tools/s3setup -endpoint http://localhost:19070 \
//	  -bucket coreban-attachments -access KEY -secret SECRET \
//	  -origins http://localhost:3001
package main

import (
	"context"
	"flag"
	"log"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/cors"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func main() {
	endpoint := flag.String("endpoint", "", "S3 endpoint URL")
	bucket := flag.String("bucket", "coreban-attachments", "bucket to create")
	access := flag.String("access", "", "access key id")
	secret := flag.String("secret", "", "secret access key")
	region := flag.String("region", "us-east-1", "region")
	origins := flag.String("origins", "", "comma-separated origins allowed to upload and read")
	flag.Parse()

	u, err := url.Parse(*endpoint)
	if err != nil || u.Host == "" {
		log.Fatalf("s3setup: -endpoint must be a URL, got %q", *endpoint)
	}
	c, err := minio.New(u.Host, &minio.Options{
		Creds: credentials.NewStaticV4(*access, *secret, ""), Secure: u.Scheme == "https", Region: *region,
	})
	if err != nil {
		log.Fatalf("s3setup: %v", err)
	}
	ctx := context.Background()
	if exists, err := c.BucketExists(ctx, *bucket); err != nil {
		log.Fatalf("s3setup: %v", err)
	} else if !exists {
		if err := c.MakeBucket(ctx, *bucket, minio.MakeBucketOptions{Region: *region}); err != nil {
			log.Fatalf("s3setup: make bucket: %v", err)
		}
	}
	rule := cors.Rule{
		AllowedOrigin: strings.Split(*origins, ","),
		AllowedMethod: []string{"GET", "PUT", "HEAD"},
		AllowedHeader: []string{"content-type"},
		ExposeHeader:  []string{"etag", "content-length"},
		MaxAgeSeconds: 3600,
	}
	if err := c.SetBucketCors(ctx, *bucket, cors.NewConfig([]cors.Rule{rule})); err != nil {
		log.Fatalf("s3setup: cors: %v", err)
	}
}
