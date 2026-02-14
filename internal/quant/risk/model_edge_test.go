package risk

import (
	"math"
	"testing"
)

// Edge Case Tests - Numerical Stability & Limit Behavior

// 1. T → 0: Price should converge to intrinsic value
func TestEdgeCase_ExpiryConvergence(t *testing.T) {
	testCases := []struct {
		name              string
		S, K              float64
		isCall            bool
		expectedIntrinsic float64
	}{
		{"ITM_Call_Expiry", 110, 100, true, 10.0},
		{"OTM_Call_Expiry", 90, 100, true, 0.0},
		{"ITM_Put_Expiry", 90, 100, false, 10.0},
		{"OTM_Put_Expiry", 110, 100, false, 0.0},
		{"ATM_Expiry", 100, 100, true, 0.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Very small time to expiry
			T := 1e-10
			r, sigma := 0.05, 0.20

			result := BSM(tc.isCall, tc.S, tc.K, T, r, sigma)

			if math.Abs(result.Price-tc.expectedIntrinsic) > 1e-6 {
				t.Errorf("Price not converging to intrinsic: got %.6f, expected %.6f",
					result.Price, tc.expectedIntrinsic)
			}

			// Greeks should be well-behaved (no NaN/Inf)
			if math.IsNaN(result.Delta) || math.IsInf(result.Delta, 0) {
				t.Errorf("Delta is NaN or Inf at expiry: %f", result.Delta)
			}
			if math.IsNaN(result.Gamma) || math.IsInf(result.Gamma, 0) {
				t.Errorf("Gamma is NaN or Inf at expiry: %f", result.Gamma)
			}
		})
	}
}

// 2. Vol → 0: Price should approach discounted intrinsic based on forward
func TestEdgeCase_ZeroVol(t *testing.T) {
	testCases := []struct {
		name   string
		S, K   float64
		isCall bool
	}{
		{"ITM_Call_LowVol", 110, 100, true},
		{"OTM_Call_LowVol", 90, 100, true},
		{"ATM_LowVol", 100, 100, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			T, r := 0.1, 0.05

			// Very low vol
			sigma := 1e-8
			result := BSM(tc.isCall, tc.S, tc.K, T, r, sigma)

			// Expected: discounted intrinsic based on forward
			F := tc.S * math.Exp(r*T)
			var intrinsic float64
			if tc.isCall {
				intrinsic = math.Max(0, F-tc.K)
			} else {
				intrinsic = math.Max(0, tc.K-F)
			}
			expected := intrinsic * math.Exp(-r*T)

			// Allow some tolerance since vol isn't exactly 0
			if math.Abs(result.Price-expected) > 0.01 {
				t.Errorf("Low vol price: got %.6f, expected %.6f (diff: %.6f)",
					result.Price, expected, math.Abs(result.Price-expected))
			}

			// No NaN/Inf
			assertNoNaN(t, "Price", result.Price)
			assertNoNaN(t, "Delta", result.Delta)
			assertNoNaN(t, "Gamma", result.Gamma)
		})
	}
}

// 3. Deep ITM/OTM: d1/d2 → ±∞, CDF should handle gracefully
func TestEdgeCase_DeepITM_OTM(t *testing.T) {
	testCases := []struct {
		name   string
		S, K   float64
		isCall bool
	}{
		{"VeryDeep_ITM_Call", 200, 100, true},
		{"VeryDeep_OTM_Call", 50, 100, true},
		{"VeryDeep_ITM_Put", 50, 100, false},
		{"VeryDeep_OTM_Put", 200, 100, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			T, r, sigma := 1.0, 0.05, 0.20

			result := BSM(tc.isCall, tc.S, tc.K, T, r, sigma)

			// Check for numerical stability
			assertNoNaN(t, "Price", result.Price)
			assertNoNaN(t, "Delta", result.Delta)
			assertNoNaN(t, "Gamma", result.Gamma)
			assertNoNaN(t, "Vega", result.Vega)
			assertNoNaN(t, "Theta", result.Theta)

			// Deep ITM: Delta should approach ±1
			if tc.isCall && tc.S > tc.K*1.5 {
				if result.Delta < 0.95 {
					t.Errorf("Deep ITM call delta too low: %.6f", result.Delta)
				}
			}
			if !tc.isCall && tc.S < tc.K*0.5 {
				if result.Delta > -0.95 {
					t.Errorf("Deep ITM put delta not negative enough: %.6f", result.Delta)
				}
			}

			// Deep OTM: Price and Delta should approach 0
			if tc.isCall && tc.S < tc.K*0.6 {
				if result.Price > 0.01 {
					t.Logf("Deep OTM call price: %.6f (expected near 0)", result.Price)
				}
				if result.Delta > 0.01 {
					t.Logf("Deep OTM call delta: %.6f (expected near 0)", result.Delta)
				}
			}
		})
	}
}

// 4. Very Short Maturity (1 hour): Theta/Gamma spikes, but no NaN
func TestEdgeCase_VeryShortMaturity(t *testing.T) {
	testCases := []struct {
		name   string
		S, K   float64
		isCall bool
	}{
		{"ATM_1Hour", 100, 100, true},
		{"NearATM_1Hour", 101, 100, true},
		{"SlightOTM_1Hour", 99, 100, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			T := 1.0 / 8760.0 // 1 hour
			r, sigma := 0.05, 0.20

			result := BSM(tc.isCall, tc.S, tc.K, T, r, sigma)

			// Main check: no NaN or Inf despite theta/gamma spikes
			assertNoNaN(t, "Price", result.Price)
			assertNoNaN(t, "Delta", result.Delta)
			assertNoNaN(t, "Gamma", result.Gamma)
			assertNoNaN(t, "Vega", result.Vega)
			assertNoNaN(t, "Theta", result.Theta)
			assertNoNaN(t, "Rho", result.Rho)

			// Gamma should be high but finite
			if result.Gamma > 100 {
				t.Logf("Very high gamma at 1hr: %.6f (acceptable spike)", result.Gamma)
			}

			// Theta should be large negative but finite
			if result.Theta < -1 {
				t.Logf("Large theta decay at 1hr: %.6f (acceptable spike)", result.Theta)
			}
		})
	}
}

// 5. Extreme Vol: Very high volatility should still produce valid prices
func TestEdgeCase_ExtremeVol(t *testing.T) {
	testCases := []struct {
		name   string
		sigma  float64
		S, K   float64
		isCall bool
	}{
		{"HighVol_200pct", 2.0, 100, 100, true},
		{"VeryHighVol_500pct", 5.0, 100, 100, true},
		{"ExtremeVol_1000pct", 10.0, 100, 100, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			T, r := 30.0/365.0, 0.05

			result := BSM(tc.isCall, tc.S, tc.K, T, r, tc.sigma)

			// No NaN/Inf
			assertNoNaN(t, "Price", result.Price)
			assertNoNaN(t, "Delta", result.Delta)

			// Price should increase with vol (option value)
			// For very high vol, price can approach spot for calls
			if result.Price < 0 {
				t.Errorf("Negative price at high vol: %.6f", result.Price)
			}

			t.Logf("Vol=%.0f%%: Price=%.6f, Delta=%.6f",
				tc.sigma*100, result.Price, result.Delta)
		})
	}
}

// 6. T=0 Explicit Test (BSM should handle this case explicitly)
func TestEdgeCase_ExactExpiry(t *testing.T) {
	testCases := []struct {
		name              string
		S, K              float64
		isCall            bool
		expectedIntrinsic float64
	}{
		{"ATM_at_Expiry", 100, 100, true, 0.0},
		{"ITM_Call_at_Expiry", 105, 100, true, 5.0},
		{"ITM_Put_at_Expiry", 95, 100, false, 5.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			T := 0.0
			r, sigma := 0.05, 0.20

			result := BSM(tc.isCall, tc.S, tc.K, T, r, sigma)

			// Should return intrinsic value exactly
			if math.Abs(result.Price-tc.expectedIntrinsic) > 1e-10 {
				t.Errorf("T=0 price mismatch: got %.6f, expected %.6f",
					result.Price, tc.expectedIntrinsic)
			}

			// Greeks should be zero or well-defined
			assertNoNaN(t, "Delta", result.Delta)
			assertNoNaN(t, "Gamma", result.Gamma)
			assertNoNaN(t, "Theta", result.Theta)
		})
	}
}

// Helper to check for NaN/Inf
func assertNoNaN(t *testing.T, name string, value float64) {
	t.Helper()
	if math.IsNaN(value) {
		t.Errorf("%s is NaN", name)
	}
	if math.IsInf(value, 0) {
		t.Errorf("%s is Inf: %f", name, value)
	}
}
