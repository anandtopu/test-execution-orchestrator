package flake

import (
	"math"
	"testing"
)

// Textbook values from the Wilson interval (95%, z=1.96).
func TestWilsonTextbook(t *testing.T) {
	// 5 successes out of 10 → CI roughly [0.237, 0.763]
	lower, upper := WilsonInterval(5, 10)
	if math.Abs(lower-0.237) > 0.01 {
		t.Errorf("lower = %f, want ~0.237", lower)
	}
	if math.Abs(upper-0.763) > 0.01 {
		t.Errorf("upper = %f, want ~0.763", upper)
	}
}

func TestWilsonZeroFailures(t *testing.T) {
	lower, upper := WilsonInterval(0, 100)
	if lower != 0 {
		t.Errorf("lower = %f, want 0", lower)
	}
	if upper > 0.05 {
		t.Errorf("upper = %f, want < 0.05 for 0/100", upper)
	}
}

func TestClassifyInsufficient(t *testing.T) {
	d := Classify(2, 5, Default())
	if !d.Insufficient {
		t.Errorf("5 attempts should be insufficient")
	}
	if d.IsFlaky || d.IsBroken {
		t.Errorf("insufficient should not classify")
	}
}

func TestClassifyFlaky(t *testing.T) {
	// 5 failures in 30 attempts: failure rate ~0.167; wilson_lower well above 0.05
	d := Classify(5, 30, Default())
	if !d.IsFlaky {
		t.Errorf("5/30 should be flaky; got decision %+v", d)
	}
	if d.IsBroken {
		t.Errorf("5/30 should not be broken")
	}
}

func TestClassifyBroken(t *testing.T) {
	// 99/100 failures: wilson_lower > 0.95 → broken
	d := Classify(99, 100, Default())
	if !d.IsBroken {
		t.Errorf("99/100 should be classified broken; got %+v", d)
	}
	if d.IsFlaky {
		t.Errorf("broken tests should not also be flagged flaky")
	}
}

func TestClassifyStable(t *testing.T) {
	// 1/100 failures: wilson_lower < 0.05 → not flaky
	d := Classify(1, 100, Default())
	if d.IsFlaky || d.IsBroken {
		t.Errorf("1/100 should be stable; got %+v", d)
	}
}
