package regime

import (
	"testing"
	"time"

	"volatility-engine/internal/domain/market"
)

func TestInterpolateIV30(t *testing.T) {
	// Setup: T1=20d, T2=40d
	// IV1=15%, IV2=17%
	now := time.Now()
	exp1 := now.AddDate(0, 0, 20)
	exp2 := now.AddDate(0, 0, 40)

	skews := []market.IVSkewSnapshot{
		{
			Expiry:   exp1.Format("2006-01-02"),
			TTEYears: 20.0 / 365.0,
			Metrics:  market.SkewMetrics{ATMVol: 0.15, Confidence: 0.8},
		},
		{
			Expiry:   exp2.Format("2006-01-02"),
			TTEYears: 40.0 / 365.0,
			Metrics:  market.SkewMetrics{ATMVol: 0.17, Confidence: 0.8},
		},
	}

	iv30, conf, err := InterpolateIV30(skews)
	if err != nil {
		t.Fatalf("Interpolation failed: %v", err)
	}

	// Verify IV30 is between 0.15 and 0.17 (Variance interp is non-linear but close)
	if iv30 < 0.15 || iv30 > 0.17 {
		t.Errorf("IV30 %.4f out of bounds [0.15, 0.17]", iv30)
	}
	if conf != 0.8 {
		t.Errorf("Expected confidence 0.8, got %.2f", conf)
	}
}

func TestWorkedExample(t *testing.T) {
	// Reproducing user example:
	// IV30 = 16.67% (input)
	// HistMin=11%, Max=28%, HV=14%
	// Expect Transition

	detector := NewDetector(DefaultSettings())

	// Create mock surface that results in IV30 approx 16.67%
	// Let's just give one point at 30 days = 16.67%
	skews := []market.IVSkewSnapshot{
		{
			TTEYears: 30.0 / 365.0,
			Metrics:  market.SkewMetrics{ATMVol: 0.1667, Confidence: 1.0},
		},
	}
	surface := market.IVSurfaceSnapshot{Skews: skews}

	state := detector.Detect(surface)

	// 1. Check Metrics
	// Rank = (0.1667-0.11)/(0.28-0.11) = 0.0567/0.17 = 0.3335
	if state.HistoricalContext.IVRank < 0.33 || state.HistoricalContext.IVRank > 0.34 {
		t.Errorf("IVRank mismatch. Got %.4f, expected ~0.333", state.HistoricalContext.IVRank)
	}

	// Ratio = 0.1667 / 0.14 = 1.19
	if state.RealizedVol.IVHVRatio < 1.18 || state.RealizedVol.IVHVRatio > 1.20 {
		t.Errorf("IVHVRatio mismatch. Got %.4f, expected ~1.19", state.RealizedVol.IVHVRatio)
	}

	// 2. Check Decision
	if state.Decision.Regime != market.RegimeTransition {
		t.Errorf("Expected TRANSITION, got %s", state.Decision.Regime)
	}
}

func TestHighVolScenario(t *testing.T) {
	// Scenario: High Rank/Pct, High Ratio
	detector := NewDetector(DefaultSettings())

	// Input > Max(0.28)? Let's put 0.27
	skews := []market.IVSkewSnapshot{
		{
			TTEYears: 30.0 / 365.0,
			Metrics:  market.SkewMetrics{ATMVol: 0.27, Confidence: 1.0},
		},
	}
	surface := market.IVSurfaceSnapshot{Skews: skews}

	state := detector.Detect(surface)

	if state.Decision.Regime != market.RegimeHighVol {
		t.Errorf("Expected HIGH_VOL, got %s (Rank=%.2f, Pct=%.2f, Ratio=%.2f)",
			state.Decision.Regime,
			state.HistoricalContext.IVRank,
			state.HistoricalContext.IVPercentile,
			state.RealizedVol.IVHVRatio)
	}
}
