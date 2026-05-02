package worker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/teo-dev/teo/internal/redact"
	"github.com/teo-dev/teo/pkg/adapter"
)

// recordingUploader captures the most recent Upload call so tests can assert
// the worker passed the right key + body.
type recordingUploader struct {
	calls   int
	gotKey  string
	gotBody []byte
	err     error
}

func (r *recordingUploader) Upload(_ context.Context, key string, body io.Reader, _ int64) error {
	r.calls++
	r.gotKey = key
	if body != nil {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(body)
		r.gotBody = buf.Bytes()
	}
	return r.err
}

// agentWithUploader builds the minimal Agent uploadLog needs — no DB pool.
func agentWithUploader(u *recordingUploader) *Agent {
	return &Agent{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Uploader: u,
		redactor: redact.New(),
	}
}

func TestUploadLog_KeyShapeIncludesRunShardTest(t *testing.T) {
	rec := &recordingUploader{}
	a := agentWithUploader(rec)

	r := adapter.Result{
		Stdout: []byte("hello stdout\n"),
		Stderr: []byte("hello stderr\n"),
	}
	got := a.uploadLog(context.Background(),
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
		r,
	)
	want := "runs/11111111-1111-1111-1111-111111111111/" +
		"shards/22222222-2222-2222-2222-222222222222/" +
		"tests/33333333-3333-3333-3333-333333333333/1.log"
	if got != want {
		t.Errorf("returned key = %q, want %q", got, want)
	}
	if rec.gotKey != want {
		t.Errorf("uploaded key = %q, want %q", rec.gotKey, want)
	}
	body := string(rec.gotBody)
	if !strings.Contains(body, "=== stdout ===") || !strings.Contains(body, "hello stdout") {
		t.Errorf("body missing stdout section: %q", body)
	}
	if !strings.Contains(body, "=== stderr ===") || !strings.Contains(body, "hello stderr") {
		t.Errorf("body missing stderr section: %q", body)
	}
}

func TestUploadLog_SkipsWhenStreamsEmpty(t *testing.T) {
	rec := &recordingUploader{}
	a := agentWithUploader(rec)

	got := a.uploadLog(context.Background(), "r", "s", "t", adapter.Result{
		Started: time.Now(), Finished: time.Now(),
	})
	if got != "" {
		t.Errorf("expected empty key when no logs to upload; got %q", got)
	}
	if rec.calls != 0 {
		t.Errorf("expected zero Upload calls; got %d", rec.calls)
	}
}

func TestUploadLog_RedactsSecretsBeforeUpload(t *testing.T) {
	rec := &recordingUploader{}
	a := agentWithUploader(rec)

	// AKIA-prefixed AWS access key is one of redact.New()'s targets.
	secret := "AKIAIOSFODNN7EXAMPLE"
	r := adapter.Result{
		Stdout: []byte("creds: " + secret + "\n"),
	}
	a.uploadLog(context.Background(), "r", "s", "t", r)
	if got := string(rec.gotBody); strings.Contains(got, secret) {
		t.Errorf("uploaded body still contains the AWS access key: %q", got)
	}
}

func TestUploadLog_FailureSectionAppendedWhenFailureFieldsSet(t *testing.T) {
	rec := &recordingUploader{}
	a := agentWithUploader(rec)
	r := adapter.Result{
		Stdout:         []byte("ran\n"),
		FailureMessage: "AssertionError: boom",
		FailureStack:   "File a.py\nAssertionError",
	}
	a.uploadLog(context.Background(), "r", "s", "t", r)
	body := string(rec.gotBody)
	if !strings.Contains(body, "=== failure ===") {
		t.Errorf("missing failure section: %q", body)
	}
	if !strings.Contains(body, "AssertionError: boom") {
		t.Errorf("failure body missing message: %q", body)
	}
}

func TestUploadLog_UploadErrorReturnsEmptyKeyButDoesNotPanic(t *testing.T) {
	rec := &recordingUploader{err: errors.New("network oops")}
	a := agentWithUploader(rec)

	got := a.uploadLog(context.Background(), "r", "s", "t", adapter.Result{
		Stdout: []byte("hi\n"),
	})
	if got != "" {
		t.Errorf("upload error should yield empty key; got %q", got)
	}
}
