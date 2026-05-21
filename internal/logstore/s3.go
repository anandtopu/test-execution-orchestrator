package logstore

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3 is the production Uploader (and Presigner). It wraps the AWS SDK v2
// transfermanager `Client.PutObject` which auto-promotes to multipart upload
// above `MultipartUploadThreshold` (16MB by default). For TEO logs that means
// small captures land as a single PUT, while a chatty test that streams
// hundreds of MB still uploads in parallel parts.
type S3 struct {
	bucket string
	tm     *transfermanager.Client
	client *s3.Client // retained for presigning (transfermanager doesn't expose it)
}

// NewS3 builds an S3 Uploader. region is required (the SDK demands one even
// for MinIO); endpoint is optional — pass it for MinIO / localstack and the
// client will use path-style addressing.
//
// AWS credentials come from the standard chain: env vars, shared config,
// IRSA/IMDS in-cluster. Operators choose how to wire this — TEO reads no
// credential env directly.
func NewS3(ctx context.Context, region, endpoint, bucket string) (*S3, error) {
	if bucket == "" {
		return nil, fmt.Errorf("logstore: bucket is required")
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("logstore: load aws config: %w", err)
	}
	s3Client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			// MinIO and most S3-compatibles require path-style; AWS S3 itself
			// also accepts it.
			o.UsePathStyle = true
		}
	})
	tm := transfermanager.New(s3Client)
	return &S3{bucket: bucket, tm: tm, client: s3Client}, nil
}

// Upload satisfies the Uploader contract. The transfermanager handles the
// single-PUT vs multipart decision internally based on the configured
// MultipartUploadThreshold (16MB default).
func (s *S3) Upload(ctx context.Context, key string, body io.Reader, _ int64) error {
	_, err := s.tm.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   body,
	})
	if err != nil {
		return fmt.Errorf("logstore: upload %s: %w", key, err)
	}
	return nil
}

// Download fetches the whole object at key. Used by `teo replay --from-s3` to
// read an archived assignment plan (runs/<id>/plan.json, S-05-04).
func (s *S3) Download(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("logstore: download %s: %w", key, err)
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

// Presign returns a time-limited GET URL for the object at key, satisfying the
// Presigner interface. The URL is signed with the same credential chain used
// for uploads; callers (the API log endpoint) choose ttl. Range requests
// against the returned URL are honored by S3/MinIO, which is how the viewer
// fetches just the tail of a large log.
func (s *S3) Presign(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if key == "" {
		return "", fmt.Errorf("logstore: empty key")
	}
	ps := s3.NewPresignClient(s.client)
	out, err := ps.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("logstore: presign %s: %w", key, err)
	}
	return out.URL, nil
}
