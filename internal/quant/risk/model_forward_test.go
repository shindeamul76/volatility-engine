package risk

import (
	"math"
	"testing"
)

// Forward-Based BSM Tests
// Verify forward-based pricing matches spot-based with appropriate carry

// 1. Spot+q vs Forward+DF Equivalence
func TestBSMForward_Equivalence(t *testing.T) {
	testCases := []struct {
		name          string
		S, K, T, r, q float64
		sigma         float64
		isCall        bool
	}{
		{"ATM_NoDiv", 100, 100, 30.0 / 365.0, 0.05, 0.00, 0.20, true},
		{"ATM_WithDiv", 100, 100, 30.0 / 365.0, 0.05, 0.02, 0.20, true},
		{"ITM_Call_Div", 110, 100, 1.0, 0.05, 0.03, 0.20, true},
		{"OTM_Put_Div", 110, 100, 30.0 / 365.0, 0.05, 0.01, 0.20, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Spot-based pricing with q
			// For BSM with dividends: use adjusted spot S*exp(-qT)
			spotAdj := tc.S * math.Exp(-tc.q*tc.T)
			spotResult := BSM(tc.isCall, spotAdj, tc.K, tc.T, tc.r, tc.sigma)

			// Forward-based pricing
			F := tc.S * math.Exp((tc.r-tc.q)*tc.T)
			DF := math.Exp(-tc.r * tc.T)
			fwdResult := BSMForward(tc.isCall, F, tc.K, DF, tc.T, tc.sigma)

			// Prices should match
			if math.Abs(spotResult.Price-fwdResult.Price) > 1e-6 {
				t.Errorf("Price mismatch: spot=%.8f, fwd=%.8f (diff: %.8f)",
					spotResult.Price, fwdResult.Price,
					math.Abs(spotResult.Price-fwdResult.Price))
			}

			// Note: Delta/Gamma/Vega are w.r.t. different underlyings (spot vs forward)
			// Price equivalence is the key validation
		})
	}
}

// 2. Forward BSM Put-Call Parity
func TestBSMForward_PutCallParity(t *testing.T) {
	testCases := []struct {
		name        string
		F, K, DF, T float64
	}{
		{"ATM", 100, 100, 0.9959, 30.0 / 365.0},
		{"ITM", 110, 100, 0.9512, 1.0},
		{"OTM", 95, 100, 0.9959, 30.0 / 365.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sigma := 0.20
			call := BSMForward(true, tc.F, tc.K, tc.DF, tc.T, sigma).Price
			put := BSMForward(false, tc.F, tc.K, tc.DF, tc.T, sigma).Price

			// Forward-based put-call parity: C - P = DF * (F - K)
			expected := tc.DF * (tc.F - tc.K)
			actual := call - put

			if math.Abs(actual-expected) > 1e-8 {
				t.Errorf("Put-Call Parity failed: C-P = %.8f, expected %.8f (diff: %.8f)",
					actual, expected, math.Abs(actual-expected))
			}
		})
	}
}

// 3. Forward BSM Bounds
func TestBSMForward_Bounds(t *testing.T) {
	F, K, DF, T, sigma := 100.0, 100.0, 0.9959, 30.0/365.0, 0.20

	call := BSMForward(true, F, K, DF, T, sigma).Price
	put := BSMForward(false, F, K, DF, T, sigma).Price

	// Call lower bound: DF * max(0, F - K)
	callLower := DF * math.Max(0, F-K)
	if call < callLower-1e-8 {
		t.Errorf("Call below lower bound: %.8f < %.8f", call, callLower)
	}

	// Call upper bound: DF * F
	callUpper := DF * F
	if call > callUpper+1e-8 {
		t.Errorf("Call above upper bound: %.8f > %.8f", call, callUpper)
	}

	// Put lower bound: DF * max(0, K - F)
	putLower := DF * math.Max(0, K-F)
	if put < putLower-1e-8 {
		t.Errorf("Put below lower bound: %.8f < %.8f", put, putLower)
	}

	// Put upper bound: DF * K
	putUpper := DF * K
	if put > putUpper+1e-8 {
		t.Errorf("Put above upper bound: %.8f > %.8f", put, putUpper)
	}
}

// 4. Forward BSM Zero Dividend Equivalence
func TestBSMForward_ZeroDivEquivalence(t *testing.T) {
	// When q=0, forward = spot * exp(rT), so forward pricing should match spot pricing
	S, K, T, r, sigma := 100.0, 100.0, 30.0/365.0, 0.05, 0.20

	// Spot-based (q=0)
	spotCall := BSM(true, S, K, T, r, sigma)
	spotPut := BSM(false, S, K, T, r, sigma)

	// Forward-based (q=0)
	F := S * math.Exp(r*T)
	DF := math.Exp(-r * T)
	fwdCall := BSMForward(true, F, K, DF, T, sigma)
	fwdPut := BSMForward(false, F, K, DF, T, sigma)

	// Prices should match exactly
	if math.Abs(spotCall.Price-fwdCall.Price) > 1e-8 {
		t.Errorf("Call price mismatch (q=0): spot=%.8f, fwd=%.8f",
			spotCall.Price, fwdCall.Price)
	}

	if math.Abs(spotPut.Price-fwdPut.Price) > 1e-8 {
		t.Errorf("Put price mismatch (q=0): spot=%.8f, fwd=%.8f",
			spotPut.Price, fwdPut.Price)
	}
}

// 5. Forward BSM with High Dividend Yield
func TestBSMForward_HighDividend(t *testing.T) {
	S, K, T, r, q, sigma := 100.0, 100.0, 1.0, 0.05, 0.08, 0.20

	// High dividend yield reduces forward
	F := S * math.Exp((r-q)*T)
	DF := math.Exp(-r * T)

	call := BSMForward(true, F, K, DF, T, sigma)

	// Forward < Spot due to high dividends
	if F >= S {
		t.Errorf("Forward should be less than spot with high dividends: F=%.2f, S=%.2f", F, S)
	}

	// Call price should be lower than no-dividend case
	FNoDivy := S * math.Exp(r*T)
	callNoDivy := BSMForward(true, FNoDivy, K, DF, T, sigma)

	if call.Price >= callNoDivy.Price {
		t.Errorf("High dividend call should be cheaper: %.6f >= %.6f",
			call.Price, callNoDivy.Price)
	}

	t.Logf("Dividend effect: No-div call=%.6f, With q=%.2f call=%.6f",
		callNoDivy.Price, q, call.Price)
}

// 6. Forward BSM Edge Cases
func TestBSMForward_EdgeCases(t *testing.T) {
	// T=0
	F, K, DF, sigma := 105.0, 100.0, 1.0, 0.20

	callExpiry := BSMForward(true, F, K, DF, 0, sigma)
	expectedIntrinsic := math.Max(0, F-K) * DF

	if math.Abs(callExpiry.Price-expectedIntrinsic) > 1e-10 {
		t.Errorf("T=0 price mismatch: got %.8f, expected %.8f",
			callExpiry.Price, expectedIntrinsic)
	}
}
