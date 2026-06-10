package predictor

import (
	"context"
	"log/slog"

	"github.com/teo-dev/teo/internal/model"
)

// Compile-time assertion that Fallback satisfies the Predictor interface.
var _ Predictor = (*Fallback)(nil)

// Fallback is a composite Predictor that tries the Primary (typically the ML
// MLClient) first and transparently falls back to Secondary (the always-present
// Heuristic) on ANY failure: timeout, connection error, non-200, decode error,
// or a length-mismatched/empty result.
//
// MAE-drift fallback: the Python service self-reports drift by setting
// used_fallback=true and returning cold-start predictions (the model branch is
// skipped server-side). That is a successful HTTP 200 response, so Fallback does
// NOT re-run the heuristic for it — the server already produced safe cold-start
// values. Hard MAE-drift cutover (disabling the ML primary entirely) is an
// operator action: unset TEO_PREDICTOR_ML_URL on the Run Manager, which reverts
// to the heuristic-only construction.
//
// Fallback is metrics-agnostic on purpose to keep internal/predictor a leaf
// package (no import of internal/metrics, which would create a cycle). The
// caller wires OnFallback to reg.PredictorFallback.Inc.
type Fallback struct {
	Primary   Predictor
	Secondary Predictor
	Logger    *slog.Logger
	// OnFallback is invoked exactly once each time Predict falls through from
	// Primary to Secondary. May be nil.
	OnFallback func()
}

// NewFallback constructs a Fallback. secondary must be non-nil (it is the
// safety net); primary may be nil, in which case every call falls through.
func NewFallback(primary, secondary Predictor, logger *slog.Logger) *Fallback {
	if logger == nil {
		logger = slog.Default()
	}
	return &Fallback{
		Primary:   primary,
		Secondary: secondary,
		Logger:    logger,
	}
}

// Predict tries Primary, then falls back to Secondary on failure.
func (f *Fallback) Predict(ctx context.Context, repoFullName string, tests []model.TestEntry) ([]Prediction, error) {
	if f.Primary != nil {
		preds, err := f.Primary.Predict(ctx, repoFullName, tests)
		if err == nil && len(preds) == len(tests) {
			return preds, nil
		}
		// Any error, or a result whose length doesn't match the request, is a
		// fallback. (An empty request yields len(tests)==0==len(preds), which is
		// a valid non-fallback path.)
		//
		// Default the logger at use so direct struct construction
		// (&Fallback{Primary:..., Secondary:...}, bypassing NewFallback) doesn't
		// nil-panic here.
		logger := f.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("ml predictor fallback to heuristic",
			"repo", repoFullName,
			"tests", len(tests),
			"got", len(preds),
			"err", err,
		)
		if f.OnFallback != nil {
			f.OnFallback()
		}
	}
	return f.Secondary.Predict(ctx, repoFullName, tests)
}
