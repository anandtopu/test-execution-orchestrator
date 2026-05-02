package logstore

import (
	"context"
	"strings"
	"testing"
)

func TestNoopUploaderConsumesBody(t *testing.T) {
	u := Noop()
	body := strings.NewReader("hello world")
	if err := u.Upload(context.Background(), "k", body, int64(body.Len())); err != nil {
		t.Fatalf("noop should never err: %v", err)
	}
	// Reader should be fully drained — recordResult hands its scratch buffer
	// over and assumes Upload consumed it.
	rest := make([]byte, 4)
	n, _ := body.Read(rest)
	if n != 0 {
		t.Errorf("noop left %d bytes unread", n)
	}
}

func TestNoopUploaderTolerableNilBody(t *testing.T) {
	u := Noop()
	if err := u.Upload(context.Background(), "k", nil, 0); err != nil {
		t.Errorf("nil body should be a tolerable no-op; got %v", err)
	}
}

// Compile-time check that S3 satisfies Uploader. NewS3 needs AWS config to
// actually run, so we only assert the type-conformance here. The integration
// path is exercised in CI/MinIO, not in this unit test.
func TestS3SatisfiesUploaderInterface(_ *testing.T) {
	var _ Uploader = (*S3)(nil)
}
