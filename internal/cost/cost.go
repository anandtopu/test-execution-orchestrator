// Package cost converts worker-minute usage into dollar estimates for the
// FR-709 cost dashboard. The pricing is configurable per deployment (env
// vars) — the defaults are AWS-ish ballpark numbers for a typical mid-size
// runner instance ($0.012/min spot, $0.040/min on-demand ≈ $0.72 / $2.40 per
// hour). They're not authoritative — operators with cost-explorer data should
// override.
//
// FR-709.
package cost

import (
	"os"
	"strconv"
)

// Pricer holds the per-minute rates used to value a run.
type Pricer struct {
	SpotPerMin     float64
	OnDemandPerMin float64
}

// Default rates approximate an m5.xlarge (4 vCPU / 16GB) in us-east-1 — the
// instance class TEO assumes for typical pytest/Jest workloads in the Helm
// values doc. Operators override via TEO_COST_SPOT_PER_MIN /
// TEO_COST_ONDEMAND_PER_MIN.
const (
	defaultSpotPerMin     = 0.012
	defaultOnDemandPerMin = 0.040
)

// NewFromEnv reads the two override env vars (or returns defaults).
// Invalid values fall back to the defaults rather than failing — the cost
// dashboard is informational, not load-bearing for run scheduling.
func NewFromEnv() Pricer {
	return Pricer{
		SpotPerMin:     readFloatEnv("TEO_COST_SPOT_PER_MIN", defaultSpotPerMin),
		OnDemandPerMin: readFloatEnv("TEO_COST_ONDEMAND_PER_MIN", defaultOnDemandPerMin),
	}
}

// RunCost returns the dollar cost of a single run given its spot and
// on-demand minute counts. Negative inputs are clamped to zero — a NUMERIC
// column should never go negative but the API guards against schema drift.
func (p Pricer) RunCost(spotMinutes, onDemandMinutes float64) float64 {
	if spotMinutes < 0 {
		spotMinutes = 0
	}
	if onDemandMinutes < 0 {
		onDemandMinutes = 0
	}
	return spotMinutes*p.SpotPerMin + onDemandMinutes*p.OnDemandPerMin
}

func readFloatEnv(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		return def
	}
	return f
}
