package runmanager

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

type fakeUploader struct {
	key  string
	body []byte
	err  error
}

func (f *fakeUploader) Upload(_ context.Context, key string, body io.Reader, _ int64) error {
	f.key = key
	b, _ := io.ReadAll(body)
	f.body = b
	return f.err
}

func quietManager(store *fakeUploader) *Manager {
	return &Manager{
		PlanStore: store,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestPlanObjectKey(t *testing.T) {
	if got := PlanObjectKey("abc-123"); got != "runs/abc-123/plan.json" {
		t.Fatalf("PlanObjectKey = %q", got)
	}
}

// archivePlan uploads the plan JSON at runs/<id>/plan.json (S-05-04 AC1).
func TestArchivePlanUploadsAtCanonicalKey(t *testing.T) {
	up := &fakeUploader{}
	m := quietManager(up)
	plan := []byte(`{"Version":"lpt-v1"}`)

	m.archivePlan(context.Background(), "run-123", plan)

	if up.key != "runs/run-123/plan.json" {
		t.Errorf("key = %q, want runs/run-123/plan.json", up.key)
	}
	if string(up.body) != string(plan) {
		t.Errorf("body = %q, want %q", up.body, plan)
	}
}

// No PlanStore wired → no-op, no panic.
func TestArchivePlanNoStoreIsNoop(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("archivePlan with no store panicked: %v", r)
		}
	}()
	m := &Manager{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	m.archivePlan(context.Background(), "run-1", []byte(`{}`))
}

// A failing upload is swallowed (plan still lives in Postgres) — never panics
// or propagates.
func TestArchivePlanUploadErrorSwallowed(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("archivePlan swallowed-error path panicked: %v", r)
		}
	}()
	up := &fakeUploader{err: errors.New("s3 down")}
	m := quietManager(up)
	m.archivePlan(context.Background(), "run-1", []byte(`{}`))
}
