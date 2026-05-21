//go:build integration

package logstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/teo-dev/teo/internal/testminio"
)

// makeBucketAndUploader spins up MinIO, creates a fresh bucket, and returns
// both a vanilla s3 client (for verification) and the logstore.S3 under test.
// The caller's t.Cleanup chain handles container shutdown.
func makeBucketAndUploader(t *testing.T) (*s3.Client, *S3, string) {
	t.Helper()
	endpoint, ak, sk, region, cleanup := testminio.Start(t)
	t.Cleanup(cleanup)

	// LoadDefaultConfig reads creds from env. Setenv resets after the test
	// without leaking process-wide state into sibling tests.
	t.Setenv("AWS_ACCESS_KEY_ID", ak)
	t.Setenv("AWS_SECRET_ACCESS_KEY", sk)
	t.Setenv("AWS_REGION", region)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	verifyClient := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	bucket := "teo-logstore-test"
	if _, err := verifyClient.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	up, err := NewS3(ctx, region, endpoint, bucket)
	if err != nil {
		t.Fatalf("logstore.NewS3: %v", err)
	}
	return verifyClient, up, bucket
}

func TestS3Upload_SmallObject_RoundTripsViaSinglePUT(t *testing.T) {
	verify, up, bucket := makeBucketAndUploader(t)

	const key = "runs/run-1/shards/shard-0/tests/test-1/1.log"
	want := []byte("=== stdout ===\nhello world\n=== stderr ===\nuh oh\n")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := up.Upload(ctx, key, bytes.NewReader(want), int64(len(want))); err != nil {
		t.Fatalf("upload: %v", err)
	}

	got := fetch(t, verify, bucket, key)
	if !bytes.Equal(got, want) {
		t.Errorf("roundtrip mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestS3Upload_LargeObject_PromotesToMultipart(t *testing.T) {
	// >16MB triggers transfermanager's multipart promotion (default threshold).
	const size = 17 * 1024 * 1024
	body := make([]byte, size)
	// Fill with a deterministic pattern so verification is meaningful and
	// compresses poorly (network path actually moves these bytes).
	for i := range body {
		body[i] = byte(i % 251) // 251 is prime; avoids alignment with part size
	}
	wantSum := sha256.Sum256(body)

	verify, up, bucket := makeBucketAndUploader(t)

	const key = "runs/run-2/shards/shard-0/tests/test-big/1.log"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := up.Upload(ctx, key, bytes.NewReader(body), int64(size)); err != nil {
		t.Fatalf("upload large: %v", err)
	}

	// Fetch and hash — comparing byte slices of 17MB on a failed test fills
	// the test log uselessly. Hash equality + length is enough.
	out, err := verify.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer out.Body.Close()

	hasher := sha256.New()
	n, err := io.Copy(hasher, out.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if n != size {
		t.Errorf("size = %d, want %d", n, size)
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); got != hex.EncodeToString(wantSum[:]) {
		t.Errorf("hash mismatch: got %s want %s", got, hex.EncodeToString(wantSum[:]))
	}
}

func TestS3Upload_OverwritesPreviousObjectAtSameKey(t *testing.T) {
	verify, up, bucket := makeBucketAndUploader(t)

	const key = "runs/run-3/shards/shard-0/tests/test-x/1.log"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := up.Upload(ctx, key, strings.NewReader("first"), 5); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	if err := up.Upload(ctx, key, strings.NewReader("second-write"), 12); err != nil {
		t.Fatalf("second upload: %v", err)
	}

	got := fetch(t, verify, bucket, key)
	if string(got) != "second-write" {
		t.Errorf("after second upload, got %q (expected the second body to overwrite)", got)
	}
}

// Download must read back exactly what Upload wrote (backs `teo replay --from-s3`).
func TestS3Download_RoundTrip(t *testing.T) {
	_, up, _ := makeBucketAndUploader(t)

	const key = "runs/run-9/plan.json"
	want := []byte(`{"Assignments":[],"Version":"lpt-v1"}`)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := up.Upload(ctx, key, bytes.NewReader(want), int64(len(want))); err != nil {
		t.Fatalf("upload: %v", err)
	}
	got, err := up.Download(ctx, key)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("download mismatch:\n got=%q\nwant=%q", got, want)
	}

	// A missing key must error rather than return empty bytes.
	if _, err := up.Download(ctx, "runs/does-not-exist/plan.json"); err == nil {
		t.Error("download of a missing key should error")
	}
}

// fetch GetObject + ReadAll wrapped in a t.Helper so failure messages point at
// the calling test line, not at fetch.
func fetch(t *testing.T, c *s3.Client, bucket, key string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer out.Body.Close()
	b, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return b
}
