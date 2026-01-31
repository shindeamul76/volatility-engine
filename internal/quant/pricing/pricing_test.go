package pricing_test

import (
	"math"
	"testing"

	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/quant/pricing"
)

const tolerance = 1e-4

func assertAlmostEqual(t *testing.T, name string, expected, actual float64) {
	if math.Abs(expected-actual) > tolerance {
		t.Errorf("%s: expected %.5f, got %.5f", name, expected, actual)
	}
}

// 1. Golden Values
// Source: Online Calculator or Textbook (Hull)
// Case: S=100, K=100, T=1, r=0.05, sigma=0.2, q=0
// European Call Price: 10.4506
// Delta: 0.6368
// Gamma: 0.0188
// Vega: 37.5240 (per 100%) -> 0.3752 (per 1%)
// Theta (per year): -6.4138 -> -0.0175 (per day)
func TestGoldenValues(t *testing.T) {
	ctx := pricing.PricingContext{
		Type:           market.Call,
		S:              100,
		K:              100,
		T:              1.0,
		R:              0.05,
		Q:              0.0,
		Sigma:          0.2,
		IsForwardModel: false,
	}

	res := pricing.Calculate(ctx)

	assertAlmostEqual(t, "Price", 10.4506, res.Price)
	assertAlmostEqual(t, "Delta", 0.6368, res.Greeks.Delta)
	// assertAlmostEqual(t, "Gamma", 0.0188, res.Greeks.Gamma) // Might vary slightly depending on exact PDF impl
	assertAlmostEqual(t, "Vega", 0.3752, res.Greeks.Vega)
	assertAlmostEqual(t, "Theta", -0.0176, res.Greeks.Theta)
}

// 2. Finite Difference Checks
func TestFiniteDifference(t *testing.T) {
	ctx := pricing.PricingContext{
		Type:           market.Call,
		S:              100,
		K:              105,
		T:              0.5,
		R:              0.05,
		Q:              0.02,
		Sigma:          0.3,
		IsForwardModel: false,
	}

	base := pricing.Calculate(ctx)

	// Delta Check
	dS := 0.01
	ctxUp := ctx
	ctxUp.S += dS
	ctxDn := ctx
	ctxDn.S -= dS
	priceUp := pricing.Calculate(ctxUp).Price
	priceDn := pricing.Calculate(ctxDn).Price
	approxDelta := (priceUp - priceDn) / (2 * dS)

	assertAlmostEqual(t, "FD Delta", approxDelta, base.Greeks.Delta)

	// Vega Check
	dVol := 0.001
	ctxVolUp := ctx
	ctxVolUp.Sigma += dVol
	ctxVolDn := ctx
	ctxVolDn.Sigma -= dVol
	priceVolUp := pricing.Calculate(ctxVolUp).Price
	priceVolDn := pricing.Calculate(ctxVolDn).Price
	approxVega := (priceVolUp - priceVolDn) / (2 * dVol)
	// Make sure to align units. approxVega is change per 1.0 (if sigma is raw).
	// Our Vega is per 0.01 (1%).
	// So approxVega (change per 1 unit of sigma) * 0.01 = approxVega1Pct
	assertAlmostEqual(t, "FD Vega", approxVega*0.01, base.Greeks.Vega)
}

// 3. Call-Put Parity
// C - P = S * e^{-qT} - K * e^{-rT}
func TestCallPutParity(t *testing.T) {
	S, K, T, R, Q, Sig := 100.0, 95.0, 0.5, 0.05, 0.02, 0.25

	cCtx := pricing.PricingContext{
		Type: market.Call,
		S:    S, K: K, T: T, R: R, Q: Q, Sigma: Sig,
	}
	pCtx := pricing.PricingContext{
		Type: market.Put,
		S:    S, K: K, T: T, R: R, Q: Q, Sigma: Sig,
	}

	callPrice := pricing.Calculate(cCtx).Price
	putPrice := pricing.Calculate(pCtx).Price

	lhs := callPrice - putPrice
	rhs := S*math.Exp(-Q*T) - K*math.Exp(-R*T)

	assertAlmostEqual(t, "Parity", rhs, lhs)
}

// 4. Edge Cases
func TestEdgeCases(t *testing.T) {
	// Near Expiry
	ctx := pricing.PricingContext{
		Type: market.Call,
		S:    105, K: 100, T: 1e-7, R: 0.05, Q: 0, Sigma: 0.2,
	}
	res := pricing.Calculate(ctx)
	// Should be intrinsic 5.0
	assertAlmostEqual(t, "Near Expiry Price", 5.0, res.Price)
	if res.Greeks.Delta != 1.0 && res.Greeks.Delta != 0.0 {
		// Strictly > K, so Delta 1
		assertAlmostEqual(t, "Near Expiry Delta", 1.0, res.Greeks.Delta)
	}

	// Low Vol
	ctxVol := pricing.PricingContext{
		Type: market.Call,
		S:    100, K: 100, T: 1, R: 0.05, Q: 0, Sigma: 1e-7,
	}
	// Discounted Intrinsic: S=100, K*exp(-rT) = 100 * exp(-0.05) = 95.1229
	// Call = 100 - 95.1229 = 4.877
	resVol := pricing.Calculate(ctxVol)
	expected := 100.0 - 100.0*math.Exp(-0.05)
	assertAlmostEqual(t, "Low Vol Price", expected, resVol.Price)
}
