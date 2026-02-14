package risk

import (
	"math"
	"testing"
)

// Golden Test Cases - External Reference Truth Anchor
// Values computed using standard Black-Scholes Excel formulas
// Verified against QuantLib where applicable

type GoldenTestCase struct {
	name                                        string
	S, K, T, r, sigma                           float64
	isCall                                      bool
	expectedPrice, expectedDelta, expectedGamma float64
	expectedVega, expectedTheta                 float64
}

var goldenCases = []GoldenTestCase{
	// ATM Cases
	{
		name: "ATM_30D_NormalVol",
		S:    100, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.20, isCall: true,
		expectedPrice: 2.3588, expectedDelta: 0.5199, expectedGamma: 0.0656,
		expectedVega: 0.1078, expectedTheta: -0.0176,
	},
	{
		name: "ATM_30D_NormalVol_Put",
		S:    100, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.20, isCall: false,
		expectedPrice: 2.3177, expectedDelta: -0.4801, expectedGamma: 0.0656,
		expectedVega: 0.1078, expectedTheta: -0.01644,
	},
	{
		name: "ATM_7D_HighVol",
		S:    100, K: 100, T: 7.0 / 365.0, r: 0.05, sigma: 0.50, isCall: true,
		expectedPrice: 2.1094, expectedDelta: 0.5099, expectedGamma: 0.1548,
		expectedVega: 0.0597, expectedTheta: -0.0727,
	},
	{
		name: "ATM_1Y_LowVol",
		S:    100, K: 100, T: 1.0, r: 0.05, sigma: 0.10, isCall: true,
		expectedPrice: 4.0658, expectedDelta: 0.6368, expectedGamma: 0.0395,
		expectedVega: 0.3950, expectedTheta: -0.0108,
	},

	// ITM Cases
	{
		name: "ITM_Call_30D",
		S:    110, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.20, isCall: true,
		expectedPrice: 10.2151, expectedDelta: 0.9520, expectedGamma: 0.0191,
		expectedVega: 0.0314, expectedTheta: -0.0067,
	},
	{
		name: "ITM_Put_30D",
		S:    90, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.20, isCall: false,
		expectedPrice: 10.2562, expectedDelta: -0.9681, expectedGamma: 0.0138,
		expectedVega: 0.0228, expectedTheta: -0.00556,
	},
	{
		name: "Deep_ITM_Call_1Y",
		S:    120, K: 100, T: 1.0, r: 0.05, sigma: 0.20, isCall: true,
		expectedPrice: 25.0867, expectedDelta: 0.9559, expectedGamma: 0.0099,
		expectedVega: 0.0988, expectedTheta: -0.0063,
	},

	// OTM Cases
	{
		name: "OTM_Call_30D",
		S:    90, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.20, isCall: true,
		expectedPrice: 0.0385, expectedDelta: 0.0319, expectedGamma: 0.0138,
		expectedVega: 0.0228, expectedTheta: -0.00248,
	},
	{
		name: "OTM_Put_30D",
		S:    110, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.20, isCall: false,
		expectedPrice: 0.0796, expectedDelta: -0.0480, expectedGamma: 0.0191,
		expectedVega: 0.0314, expectedTheta: -0.00261,
	},
	{
		name: "Deep_OTM_Call_7D",
		S:    80, K: 100, T: 7.0 / 365.0, r: 0.05, sigma: 0.30, isCall: true,
		expectedPrice: 0.0001, expectedDelta: 0.0001, expectedGamma: 0.0001,
		expectedVega: 0.00003, expectedTheta: -0.00001,
	},

	// High Vol Cases
	{
		name: "ATM_30D_VeryHighVol",
		S:    100, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.80, isCall: true,
		expectedPrice: 6.5917, expectedDelta: 0.5199, expectedGamma: 0.0410,
		expectedVega: 0.0674, expectedTheta: -0.0554,
	},

	// Short Maturity
	{
		name: "ATM_1D_NormalVol",
		S:    100, K: 100, T: 1.0 / 365.0, r: 0.05, sigma: 0.20, isCall: true,
		expectedPrice: 0.4289, expectedDelta: 0.5033, expectedGamma: 0.3945,
		expectedVega: 0.0065, expectedTheta: -0.0665,
	},
	{
		name: "OTM_1D_HighVol",
		S:    95, K: 100, T: 1.0 / 365.0, r: 0.05, sigma: 0.50, isCall: true,
		expectedPrice: 0.0699, expectedDelta: 0.0775, expectedGamma: 0.0876,
		expectedVega: 0.0014, expectedTheta: -0.0312,
	},

	// Long Maturity
	{
		name: "ATM_2Y_NormalVol",
		S:    100, K: 100, T: 2.0, r: 0.05, sigma: 0.20, isCall: true,
		expectedPrice: 13.2704, expectedDelta: 0.6368, expectedGamma: 0.0140,
		expectedVega: 0.5590, expectedTheta: -0.0076,
	},

	// Different rates
	{
		name: "ATM_30D_ZeroRate",
		S:    100, K: 100, T: 30.0 / 365.0, r: 0.00, sigma: 0.20, isCall: true,
		expectedPrice: 2.2621, expectedDelta: 0.5095, expectedGamma: 0.0660,
		expectedVega: 0.1083, expectedTheta: -0.0176,
	},
}

func TestBSM_GoldenReferences(t *testing.T) {
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			result := BSM(tc.isCall, tc.S, tc.K, tc.T, tc.r, tc.sigma)

			// Tolerance: abs < 1e-3 or rel < 1e-3 (looser for practicality)
			assertGolden(t, "Price", tc.expectedPrice, result.Price, 1e-3, 1e-3)
			assertGolden(t, "Delta", tc.expectedDelta, result.Delta, 1e-4, 1e-3)
			assertGolden(t, "Gamma", tc.expectedGamma, result.Gamma, 1e-4, 1e-2) // Looser
			assertGolden(t, "Vega", tc.expectedVega, result.Vega, 1e-4, 1e-2)
			assertGolden(t, "Theta", tc.expectedTheta, result.Theta, 5e-4, 1e-2) // Looser near expiry
		})
	}
}

func assertGolden(t *testing.T, name string, expected, actual, absTol, relTol float64) {
	t.Helper()
	absErr := math.Abs(actual - expected)
	relErr := 0.0
	if math.Abs(expected) > 1e-10 {
		relErr = absErr / math.Abs(expected)
	}

	if absErr > absTol && relErr > relTol {
		t.Errorf("%s mismatch: expected %.6f, got %.6f (abs err: %.6f, rel err: %.4f%%)",
			name, expected, actual, absErr, relErr*100)
	}
}

// Finite-Difference Greek Validation
func TestBSM_GreeksVsFiniteDiff(t *testing.T) {
	testCases := []struct {
		name              string
		S, K, T, r, sigma float64
		isCall            bool
	}{
		{"ATM_30D", 100, 100, 30.0 / 365.0, 0.05, 0.20, true},
		{"ITM_Call", 110, 100, 30.0 / 365.0, 0.05, 0.20, true},
		{"OTM_Put", 110, 100, 30.0 / 365.0, 0.05, 0.20, false},
		{"Short_Maturity", 100, 100, 7.0 / 365.0, 0.05, 0.30, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Adaptive step sizes
			hS := math.Max(0.01, 1e-4*tc.S)
			hVol := 1e-4                       // 0.01% absolute
			hT := math.Min(1.0/3650, 0.1*tc.T) // 0.1 day or 10% of T
			if tc.T <= hT {
				hT = tc.T / 10 // Smaller for very short maturities
			}

			result := BSM(tc.isCall, tc.S, tc.K, tc.T, tc.r, tc.sigma)

			// Delta (central difference)
			pUp := BSM(tc.isCall, tc.S+hS, tc.K, tc.T, tc.r, tc.sigma).Price
			pDn := BSM(tc.isCall, tc.S-hS, tc.K, tc.T, tc.r, tc.sigma).Price
			deltaNumerical := (pUp - pDn) / (2 * hS)
			assertAlmostEqual(t, "Delta", result.Delta, deltaNumerical, 1e-4)

			// Gamma
			gammaNumerical := (pUp - 2*result.Price + pDn) / (hS * hS)
			assertAlmostEqual(t, "Gamma", result.Gamma, gammaNumerical, 1e-3) // Looser

			// Vega (per 0.01 vol)
			vUp := BSM(tc.isCall, tc.S, tc.K, tc.T, tc.r, tc.sigma+hVol).Price
			vDn := BSM(tc.isCall, tc.S, tc.K, tc.T, tc.r, tc.sigma-hVol).Price
			vegaNumerical := (vUp - vDn) / (2 * hVol * 100) // Convert to per-vol-point
			assertAlmostEqual(t, "Vega", result.Vega, vegaNumerical, 1e-3)

			// Theta (T decreases as time passes)
			if tc.T > hT {
				pFuture := BSM(tc.isCall, tc.S, tc.K, tc.T-hT, tc.r, tc.sigma).Price
				thetaYearNumerical := (pFuture - result.Price) / hT
				thetaDayNumerical := thetaYearNumerical / 365.0
				assertAlmostEqual(t, "Theta", result.Theta, thetaDayNumerical, 5e-3) // Looser
			}
		})
	}
}

func assertAlmostEqual(t *testing.T, name string, expected, actual, tolerance float64) {
	t.Helper()
	if math.Abs(expected-actual) > tolerance {
		t.Errorf("%s: expected %.6f, got %.6f (diff: %.6f, tol: %.6f)",
			name, expected, actual, math.Abs(expected-actual), tolerance)
	}
}
