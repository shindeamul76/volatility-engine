package risk

import (
	"math"
	"testing"
)

// Property-Based Tests - Mathematical Truths
// These don't require external references, just verify mathematical identities

// 1. Put-Call Parity
func TestPutCallParity(t *testing.T) {
	testCases := []struct {
		name          string
		S, K, T, r, q float64
	}{
		{"ATM_30D", 100, 100, 30.0 / 365.0, 0.05, 0},
		{"ITM_1Y", 110, 100, 1.0, 0.05, 0},
		{"OTM_7D", 95, 100, 7.0 / 365.0, 0.05, 0},
		{"LongMaturity", 100, 100, 2.0, 0.05, 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sigma := 0.20
			call := BSM(true, tc.S, tc.K, tc.T, tc.r, sigma).Price
			put := BSM(false, tc.S, tc.K, tc.T, tc.r, sigma).Price

			// For q=0: C - P = S - K*exp(-rT)
			// General: C - P = S*exp(-qT) - K*exp(-rT)
			expected := tc.S*math.Exp(-tc.q*tc.T) - tc.K*math.Exp(-tc.r*tc.T)
			actual := call - put

			if math.Abs(actual-expected) > 1e-8 {
				t.Errorf("Put-Call Parity failed: C-P = %.8f, expected %.8f (diff: %.8f)",
					actual, expected, math.Abs(actual-expected))
			}
		})
	}
}

// 2. Bounds Tests
func TestBounds(t *testing.T) {
	testCases := []struct {
		name       string
		S, K, T, r float64
	}{
		{"ATM", 100, 100, 30.0 / 365.0, 0.05},
		{"ITM_Call", 110, 100, 30.0 / 365.0, 0.05},
		{"OTM_Call", 90, 100, 30.0 / 365.0, 0.05},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sigma := 0.20
			call := BSM(true, tc.S, tc.K, tc.T, tc.r, sigma).Price
			put := BSM(false, tc.S, tc.K, tc.T, tc.r, sigma).Price

			// Call bounds (q=0)
			lowerBound := math.Max(0, tc.S-tc.K*math.Exp(-tc.r*tc.T))
			upperBound := tc.S

			if call < lowerBound-1e-8 {
				t.Errorf("Call below lower bound: %.6f < %.6f", call, lowerBound)
			}
			if call > upperBound+1e-8 {
				t.Errorf("Call above upper bound: %.6f > %.6f", call, upperBound)
			}

			// Put bounds
			putLowerBound := math.Max(0, tc.K*math.Exp(-tc.r*tc.T)-tc.S)
			putUpperBound := tc.K * math.Exp(-tc.r*tc.T)

			if put < putLowerBound-1e-8 {
				t.Errorf("Put below lower bound: %.6f < %.6f", put, putLowerBound)
			}
			if put > putUpperBound+1e-8 {
				t.Errorf("Put above upper bound: %.6f > %.6f", put, putUpperBound)
			}
		})
	}
}

// 3. Monotonicity in Spot
func TestMonotonicity_Spot(t *testing.T) {
	K, T, r, sigma := 100.0, 30.0/365.0, 0.05, 0.20

	// Call should increase with S
	spots := []float64{80, 90, 100, 110, 120}
	var prevCallPrice float64
	for i, S := range spots {
		call := BSM(true, S, K, T, r, sigma).Price
		if i > 0 && call <= prevCallPrice {
			t.Errorf("Call not increasing with S: S=%.0f price=%.6f, prev=%.6f",
				S, call, prevCallPrice)
		}
		prevCallPrice = call
	}

	// Put should decrease with S
	var prevPutPrice float64 = 1e9
	for _, S := range spots {
		put := BSM(false, S, K, T, r, sigma).Price
		if put >= prevPutPrice {
			t.Errorf("Put not decreasing with S: S=%.0f price=%.6f, prev=%.6f",
				S, put, prevPutPrice)
		}
		prevPutPrice = put
	}
}

// 4. Monotonicity in Vol
func TestMonotonicity_Vol(t *testing.T) {
	S, K, T, r := 100.0, 100.0, 30.0/365.0, 0.05

	vols := []float64{0.10, 0.20, 0.30, 0.50, 0.80}

	// Both call and put increase with vol
	var prevCallPrice, prevPutPrice float64
	for i, sigma := range vols {
		call := BSM(true, S, K, T, r, sigma).Price
		put := BSM(false, S, K, T, r, sigma).Price

		if i > 0 {
			if call <= prevCallPrice {
				t.Errorf("Call not increasing with vol: σ=%.2f price=%.6f, prev=%.6f",
					sigma, call, prevCallPrice)
			}
			if put <= prevPutPrice {
				t.Errorf("Put not increasing with vol: σ=%.2f price=%.6f, prev=%.6f",
					sigma, put, prevPutPrice)
			}
		}
		prevCallPrice = call
		prevPutPrice = put
	}
}

// 5. Monotonicity in Time
func TestMonotonicity_Time(t *testing.T) {
	S, K, r, sigma := 100.0, 100.0, 0.05, 0.20

	times := []float64{1.0 / 365, 7.0 / 365, 30.0 / 365, 90.0 / 365, 1.0}

	// Generally both increase with T (edge cases exist with deep ITM and high carry)
	var prevCallPrice, prevPutPrice float64
	for i, T := range times {
		call := BSM(true, S, K, T, r, sigma).Price
		put := BSM(false, S, K, T, r, sigma).Price

		if i > 0 {
			// Allow small violations for numerical noise
			if call < prevCallPrice-1e-6 {
				t.Errorf("Call decreasing with T: T=%.4f price=%.6f, prev=%.6f",
					T, call, prevCallPrice)
			}
			if put < prevPutPrice-1e-6 {
				t.Errorf("Put decreasing with T: T=%.4f price=%.6f, prev=%.6f",
					T, put, prevPutPrice)
			}
		}
		prevCallPrice = call
		prevPutPrice = put
	}
}

// 6. Greek Identities
func TestGreekIdentities(t *testing.T) {
	testCases := []struct {
		name       string
		S, K, T, r float64
	}{
		{"ATM", 100, 100, 30.0 / 365.0, 0.05},
		{"ITM", 110, 100, 30.0 / 365.0, 0.05},
		{"OTM", 90, 100, 30.0 / 365.0, 0.05},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sigma := 0.20
			call := BSM(true, tc.S, tc.K, tc.T, tc.r, sigma)
			put := BSM(false, tc.S, tc.K, tc.T, tc.r, sigma)

			// Gamma should be same for call and put
			if math.Abs(call.Gamma-put.Gamma) > 1e-8 {
				t.Errorf("Gamma mismatch: call=%.8f, put=%.8f", call.Gamma, put.Gamma)
			}

			// Vega should be same for call and put
			if math.Abs(call.Vega-put.Vega) > 1e-8 {
				t.Errorf("Vega mismatch: call=%.8f, put=%.8f", call.Vega, put.Vega)
			}

			// Put delta = Call delta - exp(-qT) ≈ Call delta - 1 (for q=0)
			q := 0.0
			expectedPutDelta := call.Delta - math.Exp(-q*tc.T)
			if math.Abs(put.Delta-expectedPutDelta) > 1e-8 {
				t.Errorf("Delta identity failed: put Δ=%.8f, expected %.8f (call Δ - 1)",
					put.Delta, expectedPutDelta)
			}
		})
	}
}

// 7. Homogeneity (Scale Invariance)
func TestHomogeneity(t *testing.T) {
	T, r, sigma := 30.0/365.0, 0.05, 0.20

	// Test scaling by various factors
	scales := []float64{0.5, 2.0, 10.0}

	for _, scale := range scales {
		S1, K1 := 100.0, 100.0
		S2, K2 := S1*scale, K1*scale

		call1 := BSM(true, S1, K1, T, r, sigma).Price
		call2 := BSM(true, S2, K2, T, r, sigma).Price

		put1 := BSM(false, S1, K1, T, r, sigma).Price
		put2 := BSM(false, S2, K2, T, r, sigma).Price

		// Price should scale linearly
		expectedCall2 := call1 * scale
		expectedPut2 := put1 * scale

		if math.Abs(call2-expectedCall2)/math.Max(1, call2) > 1e-6 {
			t.Errorf("Call homogeneity failed: scale=%.1f, price=%.6f, expected=%.6f",
				scale, call2, expectedCall2)
		}
		if math.Abs(put2-expectedPut2)/math.Max(1, put2) > 1e-6 {
			t.Errorf("Put homogeneity failed: scale=%.1f, price=%.6f, expected=%.6f",
				scale, put2, expectedPut2)
		}
	}
}

// 8. K-Monotonicity
func TestMonotonicity_Strike(t *testing.T) {
	S, T, r, sigma := 100.0, 30.0/365.0, 0.05, 0.20

	strikes := []float64{80, 90, 100, 110, 120}

	// Call decreases with K
	var prevCallPrice float64 = 1e9
	for _, K := range strikes {
		call := BSM(true, S, K, T, r, sigma).Price
		if call >= prevCallPrice {
			t.Errorf("Call not decreasing with K: K=%.0f price=%.6f, prev=%.6f",
				K, call, prevCallPrice)
		}
		prevCallPrice = call
	}

	// Put increases with K
	var prevPutPrice float64
	for i, K := range strikes {
		put := BSM(false, S, K, T, r, sigma).Price
		if i > 0 && put <= prevPutPrice {
			t.Errorf("Put not increasing with K: K=%.0f price=%.6f, prev=%.6f",
				K, put, prevPutPrice)
		}
		prevPutPrice = put
	}
}
