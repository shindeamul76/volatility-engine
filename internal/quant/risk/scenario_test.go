package risk

import (
	"math"
	"testing"

	"volatility-engine/internal/domain/market"
)

func TestRunScenarios_BaseCasePrecision(t *testing.T) {
	// 1. Setup Mock Data
	// Spot = 100
	// Rate = 0.05
	// Expiry TTE = 0.1 years (~36 days)
	// Forward = 100 * exp(0.05 * 0.1) = 100.501 (Risk Neutral) or different if Dividend.
	// Let's assume Forward = 102.0 (High Carry) to test the fix.

	spot := 100.0
	rate := 0.05
	expiryStr := "2024-02-17"
	tte := 0.1
	forward := 102.0 // Distinct from Spot

	// Skew Snapshot
	skew := market.IVSkewSnapshot{
		Expiry:   expiryStr,
		TTEYears: tte,
		Forward:  forward,
		Fit: market.SkewFit{
			Params: market.SkewFitParams{A: 0.04, B: 0, C: 0}, // Flat Vol = 20% (Variance 0.04)
		},
	}

	skews := map[string]market.IVSkewSnapshot{
		expiryStr: skew,
	}

	// Create Strategy: Long Call ATM (Strike = Forward? or Spot?)
	// Strike = 100
	// IV = 0.20
	// Mark: Logic must calculate mark consistent with Forward=102, Vol=0.20, T=0.1, r=0.05.
	// BSM(F*exp(-rT), K, T, r, sigma) => Black76(F, K, T, r, sigma).
	// Call Price Black76:
	// d1 = (ln(F/K) + 0.5*sigma^2*T) / (sigma*sqrt(T))
	// d2 = d1 - sigma*sqrt(T)
	// c = exp(-rT) * [F*N(d1) - K*N(d2)]

	K := 100.0
	sigma := 0.20
	T := 0.1

	// Calculate theoretical mark
	d1 := (math.Log(forward/K) + 0.5*sigma*sigma*T) / (sigma * math.Sqrt(T))
	d2 := d1 - sigma*math.Sqrt(T)

	nd1 := 0.5 * (1 + math.Erf(d1/math.Sqrt(2)))
	nd2 := 0.5 * (1 + math.Erf(d2/math.Sqrt(2)))

	mark := math.Exp(-rate*T) * (forward*nd1 - K*nd2)

	candidate := market.StrategyCandidate{
		Expiry: expiryStr,
		Legs: []market.StrategyLeg{
			{
				Side:   "BUY",
				Type:   market.Call,
				Strike: K,
				Qty:    1,
				Mark:   mark,
				IV:     sigma,
			},
		},
	}

	// 2. Run Scenarios (Default Grid -> Empty TimeSteps)
	grid := DefaultScenarioGrid()
	// Force only base case spot/vol shocks for purity check, plus one shock for expiry check
	grid.SpotShocks = []float64{0, 0.10}
	grid.VolShocksAbs = []float64{0}

	res := RunScenarios(candidate, spot, rate, skews, grid)

	// 3. Verify Base Case
	// PnL should be 0.0 (or extremely close)
	// EstimatedFwd should be 102.0

	base := res.BaseCase

	if math.Abs(base.PnL) > 1e-4 {
		t.Errorf("Base Case PnL mismatch. Wanted ~0, got %f. StrategyValue: %f, InitialValue: %f", base.PnL, base.StrategyValue, mark)
	}

	if math.Abs(base.EstimatedFwd-forward) > 1e-6 {
		t.Errorf("Base Case EstimatedFwd mismatch. Wanted %f, got %f", forward, base.EstimatedFwd)
	}

	// 4. Verify Time Steps
	// Should have generated dynamic points.
	// MaxDTE = 0.1 * 365 = 36.5 days.
	// Expected: [0, 18, 36 (expiry)] or similar.

	if len(res.GridSpec.TimeSteps) == 0 {
		t.Errorf("TimeSteps not generated dynamically")
	}

	t.Logf("Generated TimeSteps: %v", res.GridSpec.TimeSteps)

	// Check Expiry Step logic
	lastStep := res.GridSpec.TimeSteps[len(res.GridSpec.TimeSteps)-1]
	// Verify scenarios at last step use Intrinsic Logic
	// At expiry, if Spot=100, Call(100) = 0.
	// If Spot Shock +10% => Spot=110 => Call(100) = 10.

	// Find result for last step, +10% spot
	foundExpiryScen := false
	for _, r := range res.Results {
		if r.Scenario.DaysForward == lastStep && r.Scenario.SpotChangePct == 0.10 {
			// Spot = 110
			// Value should be Max(0, 110-100) = 10.
			if math.Abs(r.StrategyValue-10.0) < 1e-2 {
				foundExpiryScen = true
			} else {
				t.Errorf("Expiry Scenario Value mismatch. Spot 110, Strike 100. Wanted 10.0, got %f", r.StrategyValue)
			}
		}
	}

	if !foundExpiryScen {
		t.Errorf("Did not find expected Expiry Scenario (+10%% spot)")
	}
}
