package testharness

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ejsadiarin/coreban/internal/attachment"
)

// The test S3: versitygw serving a directory, the same API R2 speaks.
const (
	s3Image  = "versity/versitygw:v1.0.17"
	s3Key    = "coreban-test"
	s3Secret = "coreban-test-secret"
	s3Region = "us-east-1"
	s3Bucket = "coreban-attachments"
)

var (
	bucketOnce sync.Once
	bucket     *attachment.Bucket
	bucketErr  error
)

// Bucket returns an attachment bucket on a disposable S3 shared by every
// test in the binary. It skips under -short. Keys carry the owner's id, so
// tests that each create their own owner never collide.
func Bucket(t *testing.T) *attachment.Bucket {
	t.Helper()
	if testing.Short() {
		t.Skip("docker-backed integration test")
	}
	bucketOnce.Do(func() { bucket, bucketErr = startBucket(context.Background()) })
	if bucketErr != nil {
		t.Fatalf("testharness: start s3: %v", bucketErr)
	}
	return bucket
}

func startBucket(ctx context.Context) (*attachment.Bucket, error) {
	ctr, err := testcontainers.Run(ctx, s3Image,
		testcontainers.WithCmd("--access", s3Key, "--secret", s3Secret, "--region", s3Region, "posix", "/tmp"),
		testcontainers.WithExposedPorts("7070/tcp"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("7070/tcp").WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		return nil, err
	}
	endpoint, err := ctr.PortEndpoint(ctx, "7070/tcp", "")
	if err != nil {
		return nil, err
	}
	admin, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(s3Key, s3Secret, ""), Region: s3Region,
	})
	if err != nil {
		return nil, err
	}
	if err := admin.MakeBucket(ctx, s3Bucket, minio.MakeBucketOptions{Region: s3Region}); err != nil {
		return nil, fmt.Errorf("make bucket: %w", err)
	}
	return attachment.NewBucket(attachment.BucketConfig{
		Endpoint: "http://" + endpoint, Bucket: s3Bucket,
		AccessKeyID: s3Key, SecretKey: s3Secret, Region: s3Region,
	})
}
