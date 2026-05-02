// Package flake implements the Wilson-interval flake detector (ADR-0011).
package flake

import "math"

// WilsonInterval returns the Wilson score 95% confidence interval for a
// binomial proportion with `failures` failures out of `n` attempts.
// At n=0 returns (0, 0).
func WilsonInterval(failures, n int) (lower, upper float64) {
	if n == 0 {
		return 0, 0
	}
	const z = 1.96
	p := float64(failures) / float64(n)
	denom := 1 + z*z/float64(n)
	center := (p + z*z/(2*float64(n))) / denom
	margin := z * math.Sqrt(p*(1-p)/float64(n)+z*z/(4*float64(n)*float64(n))) / denom
	lower = math.Max(0, center-margin)
	upper = math.Min(1, center+margin)
	return
}

// Decision summarizes whether a test should be promoted to flaky.
type Decision struct {
	NumAttempts  int
	NumFailures  int
	FailureRate  float64
	WilsonLower  float64
	WilsonUpper  float64
	IsFlaky      bool
	IsBroken     bool // ~100% failure: not flaky, broken
	Insufficient bool
}

// Threshold tunes the classifier.
type Threshold struct {
	MinSamples         int     // need at least this many runs
	WilsonLowerCutoff  float64 // promote to flaky when wilson_lower exceeds this
	BrokenLowerCutoff  float64 // wilson_lower > this means "broken", not flaky
}

// Default returns sensible defaults: 20 samples, 0.05 flaky cutoff, 0.90 broken cutoff.
// Broken cutoff is below 0.95 deliberately — Wilson-lower for 99/100 is ~0.945, so a
// 0.95 threshold would miss obvious 99% failure cases.
func Default() Threshold {
	return Threshold{
		MinSamples:        20,
		WilsonLowerCutoff: 0.05,
		BrokenLowerCutoff: 0.90,
	}
}

// Classify returns a Decision for the test.
func Classify(failures, n int, t Threshold) Decision {
	d := Decision{
		NumAttempts: n,
		NumFailures: failures,
	}
	if n < t.MinSamples {
		d.Insufficient = true
		return d
	}
	d.FailureRate = float64(failures) / float64(n)
	d.WilsonLower, d.WilsonUpper = WilsonInterval(failures, n)
	if d.WilsonLower > t.BrokenLowerCutoff {
		d.IsBroken = true
		return d
	}
	if d.WilsonLower > t.WilsonLowerCutoff {
		d.IsFlaky = true
	}
	return d
}
