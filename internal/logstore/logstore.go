// Package logstore stores per-test stdout/stderr captures at a stable
// content-addressed key (the worker's recordResult path persists this key
// into teo.test_executions.log_object_key for later retrieval).
//
// Two implementations live here:
//   - S3, backed by the AWS SDK v2 transfermanager (auto-multipart above 16MB)
//   - Noop, which discards uploads — used in dev / tests / when S3 isn't
//     configured.
//
// FR-404 in the requirements doc.
package logstore

import (
	"context"
	"io"
)

// Uploader stores a log blob at the given key.
//
// Callers pass an io.Reader plus the byte size; the size is informational
// (lets the underlying S3 client choose between a single PUT and a multipart
// stream without reading the body twice). Implementations must consume the
// reader fully before returning.
type Uploader interface {
	Upload(ctx context.Context, key string, body io.Reader, size int64) error
}

// Noop returns an Uploader that discards uploads. Convenient for dev and tests
// where the surrounding code still calls Upload but no S3 is reachable.
func Noop() Uploader { return noopUploader{} }

type noopUploader struct{}

func (noopUploader) Upload(_ context.Context, _ string, body io.Reader, _ int64) error {
	if body != nil {
		_, _ = io.Copy(io.Discard, body)
	}
	return nil
}
