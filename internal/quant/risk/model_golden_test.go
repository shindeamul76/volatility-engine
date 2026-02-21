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
	{
		name: "ATM_30D_NormalVol",
		S:    100, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.20, isCall: true,
		expectedPrice: 2.4934, expectedDelta: 0.5400, expectedGamma: 0.0692,
		expectedVega: 0.1138, expectedTheta: -0.04499,
	},
	{
		name: "ATM_30D_NormalVol_Put",
		S:    100, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.20, isCall: false,
		expectedPrice: 2.0833, expectedDelta: -0.4600, expectedGamma: 0.0692,
		expectedVega: 0.1138, expectedTheta: -0.03135,
	},
	{
		name: "ATM_7D_HighVol",
		S:    100, K: 100, T: 7.0 / 365.0, r: 0.05, sigma: 0.50, isCall: true,
		expectedPrice: 2.8087, expectedDelta: 0.5193, expectedGamma: 0.0575,
		expectedVega: 0.0552, expectedTheta: -0.20381,
	},
	{
		name: "ATM_1Y_LowVol",
		S:    100, K: 100, T: 1.0, r: 0.05, sigma: 0.10, isCall: true,
		expectedPrice: 6.8050, expectedDelta: 0.7088, expectedGamma: 0.0343,
		expectedVega: 0.3429, expectedTheta: -0.01348,
	},

	// ITM Cases
	{
		name: "ITM_Call_30D",
		S:    110, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.20, isCall: true,
		expectedPrice: 10.5111, expectedDelta: 0.9610, expectedGamma: 0.0134,
		expectedVega: 0.0266, expectedTheta: -0.02191,
	},
	{
		name: "ITM_Put_30D",
		S:    90, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.20, isCall: false,
		expectedPrice: 9.6743, expectedDelta: -0.9588, expectedGamma: 0.0171,
		expectedVega: 0.0228, expectedTheta: 0.00556,
	},
	{
		name: "Deep_ITM_Call_1Y",
		S:    120, K: 100, T: 1.0, r: 0.05, sigma: 0.20, isCall: true,
		expectedPrice: 26.1690, expectedDelta: 0.8965, expectedGamma: 0.0075,
		expectedVega: 0.2160, expectedTheta: -0.01707,
	},

	// OTM Cases
	{
		name: "OTM_Call_30D",
		S:    90, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.20, isCall: true,
		expectedPrice: 0.0844, expectedDelta: 0.0412, expectedGamma: 0.0171,
		expectedVega: 0.0228, expectedTheta: -0.00808,
	},
	{
		name: "OTM_Put_30D",
		S:    110, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.20, isCall: false,
		expectedPrice: 0.1010, expectedDelta: -0.0390, expectedGamma: 0.0134,
		expectedVega: 0.0266, expectedTheta: -0.00827,
	},
	{
		name: "Deep_OTM_Call_7D",
		S:    80, K: 100, T: 7.0 / 365.0, r: 0.05, sigma: 0.30, isCall: true,
		expectedPrice: 0.0001, expectedDelta: 0.0000, expectedGamma: 0.0000,
		expectedVega: 0.00003, expectedTheta: -0.00001,
	},

	// High Vol Cases
	{
		name: "ATM_30D_VeryHighVol",
		S:    100, K: 100, T: 30.0 / 365.0, r: 0.05, sigma: 0.80, isCall: true,
		expectedPrice: 9.3176, expectedDelta: 0.5527, expectedGamma: 0.0172,
		expectedVega: 0.1134, expectedTheta: -0.15746,
	},

	// Short Maturity
	{
		name: "ATM_1D_NormalVol",
		S:    100, K: 100, T: 1.0 / 365.0, r: 0.05, sigma: 0.20, isCall: true,
		expectedPrice: 0.4245, expectedDelta: 0.5073, expectedGamma: 0.3810,
		expectedVega: 0.0209, expectedTheta: -0.21567,
	},
	{
		name: "OTM_1D_HighVol",
		S:    95, K: 100, T: 1.0 / 365.0, r: 0.05, sigma: 0.50, isCall: true,
		expectedPrice: 0.0244, expectedDelta: 0.0261, expectedGamma: 0.0244,
		expectedVega: 0.0030, expectedTheta: -0.07564,
	},

	// Long Maturity
	{
		name: "ATM_2Y_NormalVol",
		S:    100, K: 100, T: 2.0, r: 0.05, sigma: 0.20, isCall: true,
		expectedPrice: 16.1268, expectedDelta: 0.6897, expectedGamma: 0.0125,
		expectedVega: 0.4991, expectedTheta: -0.01408,
	},

	// Different rates
	{
		name: "ATM_30D_ZeroRate",
		S:    100, K: 100, T: 30.0 / 365.0, r: 0.00, sigma: 0.20, isCall: true,
		expectedPrice: 2.2871, expectedDelta: 0.5114, expectedGamma: 0.0695,
		expectedVega: 0.1143, expectedTheta: -0.0381,
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
