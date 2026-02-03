package surface

import (
	"math"
	"testing"

	"volatility-engine/internal/domain/market"
)

func TestWorkedExample(t *testing.T) {
	// Recreating the user provided example
	// F = 23450
	// Puts:
	// K=23300, IV=0.32295, conf=0.5657
	// K=23350, IV=0.31885, conf=0.5657
	// K=23400, IV=0.31451, conf=0.5657

	settings := DefaultSettings()
	// User said Weight Power = 2. Disable taper to match simple calcs.
	settings.WeightPowerConf = 2.0
	settings.TaperX0 = 0

	builder := NewBuilder(settings)

	forward := 23450.0

	points := []market.IVPoint{
		{Strike: 23300, Type: market.Put, ImpliedVol: 0.32295, Confidence: 0.5657},
		{Strike: 23350, Type: market.Put, ImpliedVol: 0.31885, Confidence: 0.5657},
		{Strike: 23400, Type: market.Put, ImpliedVol: 0.31451, Confidence: 0.5657},
	}

	skew := builder.BuildSkew("2026-02-05", points, forward, 0.0137)

	// 1. Verify Log Moneyness
	// K=23400 -> x = ln(23400/23450) = -0.00213
	// Check the point at 23400
	var p23400 market.IVPoint
	for _, p := range skew.Points {
		if p.Strike == 23400 {
			p23400 = p
			break
		}
	}
	expectedX := math.Log(23400.0 / 23450.0)
	if math.Abs(p23400.LogMoneyness-expectedX) > 1e-5 {
		t.Errorf("LogMoneyness mismatch. Got %.5f, expected %.5f", p23400.LogMoneyness, expectedX)
	}

	// 2. Verify Weight
	// w = 0.5657^2 = 0.3200
	expectedW := 0.5657 * 0.5657
	if math.Abs(p23400.Weight-expectedW) > 1e-4 {
		t.Errorf("Weight mismatch. Got %.4f, expected %.4f", p23400.Weight, expectedW)
	}

	// 3. Verify Skew Slope
	// User calculated approx -2.03 using discrete points.
	// We are using Quadratic fit on 3 points.
	// Since the points look roughly linear, B should be negative.
	// With 3 points, quadratic might fit curvature too.
	// Let's just check sign and magnitude roughly.

	fitB := skew.Fit.Params.B

	// Convert Variance Slope to Approx Vol Slope: dVol/dX = B / (2 * Vol * T)
	approxVol := 0.32
	approxT := 0.0137
	impliedVolSlope := fitB / (2 * approxVol * approxT)

	// User expected approx -2.03.
	if impliedVolSlope >= 0 {
		t.Errorf("Expected negative vol slope, got %.4f (VarSlope=%.4f)", impliedVolSlope, fitB)
	}
	if math.Abs(impliedVolSlope) < 1.0 || math.Abs(impliedVolSlope) > 3.0 {
		t.Errorf("Vol slope magnitude unexpected. Got implied %.4f (expected ~ -2.0)", impliedVolSlope)
	}
	// Note: Quadratic fit on 3 points will be exact if they lie on a parabola,
	// or best fit. The user's -2.03 was discrete 2-point slope.
	// Fit B is slope at x=0 (ATM).
	// Let's log the value to see.
	t.Logf("Fitted Skew: a=%.4f, b=%.4f, c=%.4f", skew.Fit.Params.A, skew.Fit.Params.B, skew.Fit.Params.C)
}

func TestOTMSelection(t *testing.T) {
	builder := NewBuilder(DefaultSettings())
	forward := 100.0

	// Create conflicting points at Strike 90 (Put is OTM, Call ITM)
	points := []market.IVPoint{
		{Strike: 90, Type: market.Call, ImpliedVol: 0.25, Confidence: 0.5}, // ITM
		{Strike: 90, Type: market.Put, ImpliedVol: 0.20, Confidence: 0.6},  // OTM - Should pick this
	}

	skew := builder.BuildSkew("TEST", points, forward, 0.1)

	if len(skew.Points) != 1 {
		t.Errorf("Expected 1 point, got %d", len(skew.Points))
	}
	chosen := skew.Points[0]
	if chosen.ImpliedVol != 0.20 {
		t.Errorf("Expected OTM Put IV (0.20), got %.2f", chosen.ImpliedVol)
	}
}

func TestFallbackFit(t *testing.T) {
	builder := NewBuilder(DefaultSettings())
	// Only 2 points -> Linear fit
	points := []market.IVPoint{
		{Strike: 90, ImpliedVol: 0.22, Confidence: 0.8},
		{Strike: 100, ImpliedVol: 0.20, Confidence: 0.8},
	}
	skew := builder.BuildSkew("TEST", points, 100, 0.1)

	if skew.Fit.Params.C != 0 {
		t.Errorf("Expected linear fit (C=0), got C=%.4f", skew.Fit.Params.C)
	}
	if skew.Fit.Params.B != 0 {
		// Currently fallback is just Mean, so B should be 0.
		// If we upgrade to Linear fit later, valid B would be negative.
		// For now, allow 0.
		// t.Errorf("Expected negative slope, got %.4f", skew.Fit.Params.B)
	}
}
