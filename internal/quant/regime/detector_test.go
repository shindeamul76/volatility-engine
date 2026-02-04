package regime

import (
	"math"
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

	iv30, conf, _, err := InterpolateIV30(skews)
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

// Mock Providers for testing
type MockIVHistoryProvider struct {
	Data []float64
}

func (m *MockIVHistoryProvider) GetIV30History(symbol string, asOf time.Time, lookback int) ([]float64, error) {
	return m.Data, nil
}

type MockPriceHistoryProvider struct {
	Closes []float64
}

func (m *MockPriceHistoryProvider) GetDailyCloses(symbol string, asOf time.Time, lookbackDays int) ([]float64, error) {
	return m.Closes, nil
}

func TestWorkedExample(t *testing.T) {
	// Reproducing user example:
	// IV30 = 16.67% (input)
	// HistMin=11%, Max=28%
	// We want Rank ~ 0.33
	// Let's create history that yields this.
	// 11%...28%. 16.67 is approx 1/3 of the way.

	// histData := []float64{0.11, 0.28, 0.14} // Just min, max, and something else?
	// To get stable rank, let's provide a linear range or just min/max limits in the slice
	// For exact rank calculation: min=0.11, max=0.28.
	// Rank = (0.1667 - 0.11) / (0.28 - 0.11) = 0.0567 / 0.17 = 0.3335

	// Mock HV needed.
	// User said HV=14% (0.14).
	// We need 21 closes that give annual vol of 0.14.
	// Simplest: constant return r where r * sqrt(252) = 0.14 => r = 0.14/sqrt(252) ~= 0.0088
	// prices[i] = prices[i-1] * exp(r)

	closes := make([]float64, 22)
	closes[0] = 100.0
	targetAnnVol := 0.14
	dailyVol := targetAnnVol / math.Sqrt(252)
	// Actually we need standard deviation of log returns to be dailyVol.
	// If we just alternate up/down we might get it, or just use constant growth?
	// Constant growth gives 0 variance!
	// We need variance.
	// Let's alternate: +vol, -vol, +vol...
	// r = +/- dailyVol. Mean ~ 0. Variance ~ dailyVol^2.
	for i := 1; i < len(closes); i++ {
		sign := 1.0
		if i%2 == 0 {
			sign = -1.0
		}
		ret := sign * dailyVol
		closes[i] = closes[i-1] * math.Exp(ret)
	}

	ivProvider := &MockIVHistoryProvider{Data: []float64{0.11, 0.28, 0.12, 0.20}} // Includes min 0.11, max 0.28
	priceProvider := &MockPriceHistoryProvider{Closes: closes}

	detector := NewDetector(DefaultSettings(), ivProvider, priceProvider)

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

	// HV check
	// Our mock closes should give ~0.14 HV
	if math.Abs(state.RealizedVol.HV-0.14) > 0.01 {
		t.Errorf("HV mismatch. Got %.4f, expected ~0.14", state.RealizedVol.HV)
	}

	// Ratio = 0.1667 / 0.14 = 1.19
	// HV might slightly differ due to sample variance calc, but should be close.
	expectedRatio := 0.1667 / state.RealizedVol.HV
	if math.Abs(state.RealizedVol.IVHVRatio-expectedRatio) > 0.0001 {
		t.Errorf("IVHVRatio mismatch. Got %.4f, expected %.4f", state.RealizedVol.IVHVRatio, expectedRatio)
	}

	// 2. Check Decision
	if state.Decision.Regime != market.RegimeTransition {
		t.Errorf("Expected TRANSITION, got %s", state.Decision.Regime)
	}
}

func TestHighVolScenario(t *testing.T) {
	// Scenario: High Rank/Pct, High Ratio

	// Mock High IV History
	// IV30 = 0.27
	// We want Rank/Pct > 0.70.
	// If History is [0.10, 0.20], then 0.27 is Max (Rank=1.0) and Pct=1.0
	ivProvider := &MockIVHistoryProvider{Data: []float64{0.10, 0.20, 0.15}}

	// Mock Low HV so Ratio is high
	// IV=0.27. Want Ratio > 1.15 => HV < 0.27/1.15 = 0.23
	// Let's set HV ~ 0.10
	closes := make([]float64, 30)
	closes[0] = 100.0
	dailyVol := 0.10 / math.Sqrt(252)
	for i := 1; i < len(closes); i++ {
		sign := 1.0
		if i%2 == 0 {
			sign = -1.0
		}
		closes[i] = closes[i-1] * math.Exp(sign*dailyVol)
	}

	priceProvider := &MockPriceHistoryProvider{Closes: closes}

	detector := NewDetector(DefaultSettings(), ivProvider, priceProvider)

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
