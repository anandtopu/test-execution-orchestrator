package predictor

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/teo-dev/teo/internal/model"
)

// compileTimeAssertions documents (and the package's `var _` lines enforce at
// build time) that both concrete predictors satisfy the Predictor interface.
// Keeping a test reference here means the contract is also exercised under
// `go test`, not just `go build`.
func TestPredictorInterfaceSatisfied(_ *testing.T) {
	var _ Predictor = (*Fallback)(nil)
	var _ Predictor = (*MLClient)(nil)
	var _ Predictor = (*Heuristic)(nil)
}

// stubPredictor is a configurable Predictor for exercising Fallback wiring.
type stubPredictor struct {
	preds  []Prediction
	err    error
	calls  int
	lastRF string
}

func (s *stubPredictor) Predict(_ context.Context, repoFullName string, _ []model.TestEntry) ([]Prediction, error) {
	s.calls++
	s.lastRF = repoFullName
	return s.preds, s.err
}

func twoTests() []model.TestEntry {
	return []model.TestEntry{{Path: "a", Name: "T1"}, {Path: "b", Name: "T2"}}
}

func twoPreds() []Prediction {
	return []Prediction{{Fingerprint: "a::T1"}, {Fingerprint: "b::T2"}}
}

func TestFallbackUsesPrimaryOnSuccess(t *testing.T) {
	primary := &stubPredictor{preds: twoPreds()}
	secondary := &stubPredictor{preds: twoPreds()}
	fellBack := 0
	f := NewFallback(primary, secondary, nil)
	f.OnFallback = func() { fellBack++ }

	got, err := f.Predict(context.Background(), "owner/repo", twoTests())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d preds, want 2", len(got))
	}
	if secondary.calls != 0 {
		t.Errorf("secondary called %d times, want 0", secondary.calls)
	}
	if fellBack != 0 {
		t.Errorf("OnFallback fired %d times, want 0", fellBack)
	}
}

func TestFallbackOnPrimaryError(t *testing.T) {
	primary := &stubPredictor{err: errors.New("boom")}
	secondary := &stubPredictor{preds: twoPreds()}
	fellBack := 0
	f := NewFallback(primary, secondary, nil)
	f.OnFallback = func() { fellBack++ }

	got, err := f.Predict(context.Background(), "owner/repo", twoTests())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d preds, want 2", len(got))
	}
	if secondary.calls != 1 {
		t.Errorf("secondary called %d times, want 1", secondary.calls)
	}
	if fellBack != 1 {
		t.Errorf("OnFallback fired %d times, want exactly 1", fellBack)
	}
}

func TestFallbackOnLengthMismatch(t *testing.T) {
	// Primary returns a short slice (1 pred for 2 tests) -> fallback.
	primary := &stubPredictor{preds: twoPreds()[:1]}
	secondary := &stubPredictor{preds: twoPreds()}
	fellBack := 0
	f := NewFallback(primary, secondary, nil)
	f.OnFallback = func() { fellBack++ }

	got, err := f.Predict(context.Background(), "owner/repo", twoTests())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d preds, want 2 (from secondary)", len(got))
	}
	if secondary.calls != 1 {
		t.Errorf("secondary called %d times, want 1", secondary.calls)
	}
	if fellBack != 1 {
		t.Errorf("OnFallback fired %d times, want 1", fellBack)
	}
}

func TestFallbackNilPrimaryAlwaysDelegates(t *testing.T) {
	secondary := &stubPredictor{preds: twoPreds()}
	fellBack := 0
	f := NewFallback(nil, secondary, nil)
	f.OnFallback = func() { fellBack++ }

	got, err := f.Predict(context.Background(), "owner/repo", twoTests())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d preds, want 2", len(got))
	}
	if secondary.calls != 1 {
		t.Errorf("secondary called %d times, want 1", secondary.calls)
	}
	if fellBack != 0 {
		t.Errorf("OnFallback fired %d times, want 0 (nil primary is not a fallback)", fellBack)
	}
}

func TestFallbackOnPrimaryErrorLogsWarnAndFiresOnceFromBuffer(t *testing.T) {
	// Capture slog output into a buffer and assert exactly one Warn line is
	// emitted on the fallback path, alongside OnFallback firing exactly once.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	primary := &stubPredictor{err: errors.New("boom")}
	secondary := &stubPredictor{preds: twoPreds()}
	fellBack := 0
	f := NewFallback(primary, secondary, logger)
	f.OnFallback = func() { fellBack++ }

	got, err := f.Predict(context.Background(), "owner/repo", twoTests())
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, 1, secondary.calls, "secondary should be consulted once")
	require.Equal(t, 1, fellBack, "OnFallback must fire exactly once")

	out := buf.String()
	require.Equal(t, 1, strings.Count(out, "level=WARN"), "exactly one Warn line expected")
	require.Contains(t, out, "ml predictor fallback to heuristic")
	require.Contains(t, out, "repo=owner/repo")
}

func TestFallbackBothErrorReturnsHeuristicError(t *testing.T) {
	// Primary errors → fall through; secondary (heuristic) ALSO errors → the
	// heuristic's error is what propagates, and OnFallback still fires once.
	heuristicErr := errors.New("heuristic db down")
	primary := &stubPredictor{err: errors.New("ml boom")}
	secondary := &stubPredictor{err: heuristicErr}
	fellBack := 0
	f := NewFallback(primary, secondary, nil)
	f.OnFallback = func() { fellBack++ }

	got, err := f.Predict(context.Background(), "owner/repo", twoTests())
	require.Error(t, err)
	require.ErrorIs(t, err, heuristicErr, "the heuristic's error must propagate, not the ML one")
	require.Nil(t, got)
	require.Equal(t, 1, secondary.calls)
	require.Equal(t, 1, fellBack, "OnFallback fires once even when the heuristic also errors")
}

func TestFallbackPrimarySuccessReturnsVerbatim(t *testing.T) {
	// A correct-length primary result is returned verbatim; OnFallback NOT called
	// and the secondary is never consulted. (require-based mirror of the existing
	// TestFallbackUsesPrimaryOnSuccess, also asserting value identity.)
	want := []Prediction{
		{Fingerprint: "a::T1", P50DurationMS: 11, P95DurationMS: 33, FlakeProbability: 0.2},
		{Fingerprint: "b::T2", P50DurationMS: 22, P95DurationMS: 66, IsColdStart: true},
	}
	primary := &stubPredictor{preds: want}
	secondary := &stubPredictor{preds: twoPreds()}
	fellBack := 0
	f := NewFallback(primary, secondary, nil)
	f.OnFallback = func() { fellBack++ }

	got, err := f.Predict(context.Background(), "owner/repo", twoTests())
	require.NoError(t, err)
	require.Equal(t, want, got, "ML result returned verbatim")
	require.Zero(t, secondary.calls)
	require.Zero(t, fellBack)
}

func TestFallbackNilLoggerDoesNotPanic(t *testing.T) {
	// Direct struct construction bypasses NewFallback's logger default; the
	// fallback path must not nil-panic.
	f := &Fallback{
		Primary:   &stubPredictor{err: errors.New("boom")},
		Secondary: &stubPredictor{preds: twoPreds()},
	}
	if _, err := f.Predict(context.Background(), "owner/repo", twoTests()); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}
