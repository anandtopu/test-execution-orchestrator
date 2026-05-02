# ADR-0011: Wilson lower bound for flake confirmation

**Status:** Accepted
**Date:** 2026-04-30

## Context
Naive flake detection ("test failed, then passed → flaky") has high false-positive rates and quarantines stable tests after a single bad-luck run. PRD §3 commits to <1% false-positive quarantine rate.

## Decision
A test is promoted from `flaky-candidate` to `flaky` only when:
1. Sample size ≥ M (default 20) attempts in the rolling 30-day window, AND
2. Wilson score interval lower bound on the failure rate > θ (default 0.05) at 95% confidence.

The Wilson interval is the standard binomial confidence-interval method that bounds false positives at small sample sizes (better than the normal approximation). Formula:
```
center  = (p + z²/(2n)) / (1 + z²/n)
margin  = z * sqrt(p(1-p)/n + z²/(4n²)) / (1 + z²/n)
lower   = center - margin
```
where p = failure_rate, n = sample size, z = 1.96 (95%).

## Consequences
**+** Bounded false-positive rate; we can defend "we are 95% sure this is flaky."
**+** Pure math; no ML model to train or operate in MVP.
**−** Slow to confirm: a test needs ~20 runs across the window before classification. Acceptable, since urgent-quarantine is operator-action-only in MVP (FR-605).
**−** Threshold tuning is per-team in practice. We expose `flake.wilson_lower_threshold` and `flake.min_samples` in config.

## Alternatives considered
- **Simple ratio threshold.** Rejected — small-sample false positives.
- **Bayesian / Beta-Binomial.** More principled; equivalent for v1 needs but heavier explanation cost.
- **ML predictor first.** Deferred to v1.5 (ADR-0012); needs labeled data we don't yet have.
