package cost

import (
	"math"
	"testing"
)

func TestRunCostAppliesBothRates(t *testing.T) {
	p := Pricer{SpotPerMin: 0.01, OnDemandPerMin: 0.05}
	got := p.RunCost(60, 30) // 60min spot @ 0.01 + 30min on-demand @ 0.05
	want := 60*0.01 + 30*0.05
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("RunCost = %v, want %v", got, want)
	}
}

func TestRunCostZeroMinutesIsZero(t *testing.T) {
	p := Pricer{SpotPerMin: 0.01, OnDemandPerMin: 0.05}
	if got := p.RunCost(0, 0); got != 0 {
		t.Errorf("RunCost(0,0) = %v, want 0", got)
	}
}

func TestRunCostClampsNegativeInputs(t *testing.T) {
	p := Pricer{SpotPerMin: 0.01, OnDemandPerMin: 0.05}
	got := p.RunCost(-5, -10)
	if got != 0 {
		t.Errorf("RunCost(-5,-10) = %v, want 0", got)
	}
	// Mixed: one negative, one positive — only the positive contributes.
	got = p.RunCost(-1, 10)
	if math.Abs(got-0.5) > 1e-9 {
		t.Errorf("RunCost(-1,10) = %v, want 0.5", got)
	}
}

func TestNewFromEnvFallsBackToDefaultsWhenUnset(t *testing.T) {
	t.Setenv("TEO_COST_SPOT_PER_MIN", "")
	t.Setenv("TEO_COST_ONDEMAND_PER_MIN", "")
	p := NewFromEnv()
	if p.SpotPerMin != defaultSpotPerMin {
		t.Errorf("SpotPerMin = %v, want default %v", p.SpotPerMin, defaultSpotPerMin)
	}
	if p.OnDemandPerMin != defaultOnDemandPerMin {
		t.Errorf("OnDemandPerMin = %v, want default %v", p.OnDemandPerMin, defaultOnDemandPerMin)
	}
}

func TestNewFromEnvHonoursValidOverrides(t *testing.T) {
	t.Setenv("TEO_COST_SPOT_PER_MIN", "0.025")
	t.Setenv("TEO_COST_ONDEMAND_PER_MIN", "0.080")
	p := NewFromEnv()
	if math.Abs(p.SpotPerMin-0.025) > 1e-9 {
		t.Errorf("SpotPerMin = %v", p.SpotPerMin)
	}
	if math.Abs(p.OnDemandPerMin-0.080) > 1e-9 {
		t.Errorf("OnDemandPerMin = %v", p.OnDemandPerMin)
	}
}

func TestNewFromEnvIgnoresInvalidAndNegativeValues(t *testing.T) {
	t.Setenv("TEO_COST_SPOT_PER_MIN", "garbage")
	t.Setenv("TEO_COST_ONDEMAND_PER_MIN", "-0.05")
	p := NewFromEnv()
	if p.SpotPerMin != defaultSpotPerMin {
		t.Errorf("garbage value should fall back to default; got %v", p.SpotPerMin)
	}
	if p.OnDemandPerMin != defaultOnDemandPerMin {
		t.Errorf("negative value should fall back to default; got %v", p.OnDemandPerMin)
	}
}
