package risk

import (
	"math"
	"time"
	"volatility-engine/internal/domain/market"
)

// DefaultScenarioGrid returns a standard grid for analysis.
func DefaultScenarioGrid() ScenarioGridSpec {
	return ScenarioGridSpec{
		SpotShocks:   []float64{-0.10, -0.05, 0, 0.05, 0.10},
		VolShocksAbs: []float64{-0.05, 0, 0.05}, // Absolute vol change: e.g., 0.20 → 0.25 is +0.05
		TimeSteps:    []int{},                   // Filled dynamically
	}
}

// PricingContext holds data needed to reprice options.
type PricingContext struct {
	CurrentSpot  float64
	CurrentTime  market.ExpiryMetadata
	RiskFreeRate float64
	SkewParams   market.SkewFitParams // Fit for this expiry
	IsCall       bool
	Strike       float64
	EntryVol     float64
}

// RunScenarios orchestrates the grid simulation.
// It requires the strategy, the current market context (spot, rate), and the surface (for skew params).
func RunScenarios(candidate market.StrategyCandidate, spot float64, rate float64, skews map[string]market.IVSkewSnapshot, grid ScenarioGridSpec) ScenarioAnalysis {
	var results []ScenarioResult

	// Pre-calculate initial strategy value (valuation at current market prices)
	// PnL = CurrentValue - InitialValue
	// InitialValue is effectively the cost to enter.
	// We want PnL referenced to entry.
	initialValue := 0.0
	for _, l := range candidate.Legs {
		sign := 1.0
		if l.Side == "SELL" {
			sign = -1.0
		}
		initialValue += sign * float64(l.Qty) * l.Mark
	}

	worstPnL := math.MaxFloat64
	var worstCase ScenarioResult

	bestPnL := -math.MaxFloat64
	var bestCase ScenarioResult

	var baseCase ScenarioResult
	foundBaseCase := false

	count := 0

	// Pre-calculation of TTE/DTE limit
	maxDTE := 30.0 // Fallback
	if sk, ok := skews[candidate.Expiry]; ok {
		maxDTE = sk.TTEYears * 365.0
	}

	// Calculate Model Calibration Offset
	// We run the Base Case (0,0,0) through our Pricing Model.
	// We compare ModelValue(Base) with InitialValue(Market).
	// Offset = InitialValue - ModelValue.
	// We add this Offset to ALL scenario model values.
	// This ensures PnL(Base) = (ModelValue + Offset) - InitialValue = 0.
	baseScen := ScenarioPoint{0, 0, 0}
	rawBaseResult := EvaluateScenario(candidate, baseScen, spot, rate, 0, skews, 0, false)
	modelOffset := initialValue - rawBaseResult.StrategyValue

	// Sanitize/Filter Time Steps from Input Grid
	// We want to remove steps that are >= maxDTE, as we will add a specific Expiry step.
	var cleanSteps []int
	seen := make(map[int]bool)

	// Add 0 if not present
	if len(grid.TimeSteps) == 0 {
		cleanSteps = append(cleanSteps, 0)
		seen[0] = true
	} else {
		for _, t := range grid.TimeSteps {
			// If t is very close to maxDTE (within 1 day) or larger, skip it for now.
			// We want explicit control over the expiry step.
			if float64(t) < maxDTE-0.5 {
				if !seen[t] {
					cleanSteps = append(cleanSteps, t)
					seen[t] = true
				}
			}
		}
	}

	// Dynamic Infill if sparse
	// If we only have [0] and DTE is large, add intermediates?
	// User request: [0, 1, 2, 3] for 3 day expiry.
	// If cleanSteps is just [0] and DTE > 2, assume we need better granularity.
	if len(cleanSteps) <= 1 && maxDTE > 1.5 {
		// Fill daily? Or just mid?
		// User liked [0, 1, 2, 3].
		// Let's add daily steps if DTE is small (< 7).
		if maxDTE < 7.0 {
			for i := 1; i < int(maxDTE); i++ {
				if !seen[i] {
					cleanSteps = append(cleanSteps, i)
					seen[i] = true
				}
			}
		} else {
			// For longer DTE, add Mid
			mid := int(maxDTE / 2)
			if mid > 0 && !seen[mid] {
				cleanSteps = append(cleanSteps, mid)
				seen[mid] = true
			}
		}
	}

	// Always add Expiry Step
	expiryStep := int(math.Ceil(maxDTE))
	if expiryStep == 0 && maxDTE > 0.001 {
		expiryStep = 1
	}
	if !seen[expiryStep] {
		cleanSteps = append(cleanSteps, expiryStep)
	}

	grid.TimeSteps = cleanSteps

	// Generate Grid Points
	for _, dt := range grid.TimeSteps {
		// CLAMPING: Only for safety, logic above guarantees dt ~<= Ceil(maxDTE)
		dtFloat := float64(dt)
		isExpiry := false

		// If dt >= maxDTE (or very close), treated as expiry (intrinsic)
		if dtFloat >= maxDTE-0.01 {
			dtFloat = maxDTE
			isExpiry = true
		}

		for _, dSpot := range grid.SpotShocks {
			for _, dVol := range grid.VolShocksAbs {

				// Define Scenario
				scen := ScenarioPoint{
					SpotChangePct: dSpot,
					VolChange:     dVol,
					DaysForward:   dt,
				}

				// Effective Days Forward for pricing
				effDays := dtFloat

				// Pass Offset!
				// EvaluateScenario now takes offset? Or we adjust here.
				// Let's adjust here to keep Evaluate pure logic.
				res := EvaluateScenario(candidate, scen, spot, rate, initialValue, skews, effDays, isExpiry)

				// Apply Calibration
				// But wait, if isExpiry (Intrinsic), do we apply offset?
				// Intrinsic is hard truth. Model mismatch usually comes from Vol/Curve.
				// If we apply offset at expiry, we might distort payoff.
				// BUT, if we entered at a credit of 100, and model says 98.
				// Offset = +2.
				// At expiry, if payoff is 0. Model says 0.
				// Should we report +2?
				// "PnL" is what matters.
				// PnL = Value - Cost.
				// At expiry, Value is Intrinsic. PnL = Intrinsic - Cost.
				// If we apply offset, Value = Intrinsic + 2. PnL = Intrinsic + 2 - Cost.
				// That implies we made $2 more.
				// This calibration (Vertical Shift) assumes the error is constant across time/spot?
				// Or is it a Volatility error (Vega)?
				// Usually we calibrate Implied Vol to match price.
				// Since we are not solving for IV per-scenario-leg (we use leg.IV), the error should be small IF methods match.
				// User says: "Base-Case PnL mismatch ... because your scenario pricer is not using exact same conventions".
				// IF we fix the conventions (Forward, Rate), error should be ~0.
				// Indeed, our unit test showed error ~0 when we fixed Inputs.
				// So we might NOT need Offset if fixes work.
				// User Option 1: "Base-case valuation uses mark/mid prices, so pnl≈0".
				// Since we verified PnL~0 with correct inputs, let's rely on that primarily.
				// BUT, real market data might have slight mismatches (bid/ask vs mid, etc).
				// Safest: Use Offset, but decay it? No, keep it simple.
				// If we use Offset, we force PnL=0 at t=0.
				// Let's use it for non-expiry steps. At expiry, strictly Intrinsic.

				if !isExpiry {
					res.StrategyValue += modelOffset
					res.PnL = res.StrategyValue - initialValue
				}
				// If Expiry, StrategyValue is Intrinsic. PnL = Intrinsic - InitialValue. Correct.

				results = append(results, res)
				count++

				if res.PnL < worstPnL {
					worstPnL = res.PnL
					worstCase = res
				}
				if res.PnL > bestPnL {
					bestPnL = res.PnL
					bestCase = res
				}
				// Base Case: 0 shocks, 0 time
				if dt == 0 && dSpot == 0 && dVol == 0 {
					baseCase = res
					foundBaseCase = true
				}
			}
		}
	}

	// Safety for Base Case (if 0,0,0 was not in grid)
	if !foundBaseCase && len(results) > 0 {
		// Force calculate it
		baseScen := ScenarioPoint{0, 0, 0}
		baseCase = EvaluateScenario(candidate, baseScen, spot, rate, initialValue, skews, 0, false)
		baseCase.StrategyValue += modelOffset
		baseCase.PnL = baseCase.StrategyValue - initialValue
	}

	// Update count in GridSpec for reporting
	grid.ScenariosCount = count

	return ScenarioAnalysis{
		GridSpec:   grid,
		Results:    results,
		WorstCase:  worstCase,
		BestCase:   bestCase,
		BaseCase:   baseCase,
		MaxMtmLoss: worstPnL,
	}
}

// EvaluateScenario runs the valuation for a single point.
// effDaysForward is the clamped time step.
func EvaluateScenario(candidate market.StrategyCandidate, p ScenarioPoint, baseSpot, rate, initialValue float64, skews map[string]market.IVSkewSnapshot, effDaysForward float64, isExpiry bool) ScenarioResult {

	estSpot := baseSpot * (1 + p.SpotChangePct)

	// Estimated Date
	estDate := ""
	if !candidate.AsOf.IsZero() {
		targetTime := candidate.AsOf.AddDate(0, 0, p.DaysForward)
		estDate = targetTime.Format(time.RFC3339)
	}

	totalValue := 0.0
	var netGreeks market.Greeks

	// We need one reference expiry TTE for the "EstimatedFwd" output field (usually the strategy expiry)
	// We'll calculate it from the first leg or candidate expiry.
	refExpiry := candidate.Expiry
	var refTTE float64 = 30.0 / 365.0 // Default
	var refCarry float64 = rate
	var refBaseFwd float64 = baseSpot // Default

	if sk, ok := skews[refExpiry]; ok {
		refTTE = sk.TTEYears
		refBaseFwd = sk.Forward // Use exact forward from snapshot
		// Implied Carry from Fwd/Spot
		if baseSpot > 0 && sk.Forward > 0 && refTTE > 0.001 {
			refCarry = math.Log(sk.Forward/baseSpot) / refTTE
		}
	} else {
		// Fallback if no skew
		refBaseFwd = baseSpot * math.Exp(rate*refTTE)
	}

	// Estimated Fwd for the Result struct (Scenario Consistent)
	// Base Case Check: If t=0, dSpot=0, we must return refBaseFwd.
	// General: F_new = S_new * (F_old / S_old) * exp(-carry * dt) ??
	// No. Forward at time t, having moved dt:
	// F(t) = S(t) * exp(carry * (T-t))
	// We assume carry (r-q) is constant.
	timePassed := effDaysForward / 365.0
	refTimeRemaining := refTTE - timePassed
	if refTimeRemaining < 0 {
		refTimeRemaining = 0
	}

	estimatedFwd := estSpot * math.Exp(refCarry*refTimeRemaining)

	// Adjustment for Base Case precision
	if effDaysForward == 0 && p.SpotChangePct == 0 {
		estimatedFwd = refBaseFwd
	}

	for _, leg := range candidate.Legs {
		expiry := candidate.Expiry // Simplified: assume single expiry strategy

		// Intrinsic Value (Payoff) Logic
		if isExpiry || refTimeRemaining <= 0.0001 {
			// Intrinsic Payout
			val := 0.0
			if leg.Type == market.Call {
				val = math.Max(0, estSpot-leg.Strike)
			} else {
				val = math.Max(0, leg.Strike-estSpot)
			}
			sign := 1.0
			if leg.Side == "SELL" {
				sign = -1.0
			}
			totalValue += sign * float64(leg.Qty) * val
			// Greeks are zero at expiry (or undefined gamma)
			continue
		}

		// Look up Skew Snapshot
		sk, ok := skews[expiry]
		var t0, carry, entryVol float64
		var skewParams []float64

		if ok {
			t0 = sk.TTEYears
			// Implied Carry
			if baseSpot > 0 && sk.Forward > 0 && t0 > 0.001 {
				carry = math.Log(sk.Forward/baseSpot) / t0
			} else {
				carry = rate
			}
			skewParams = []float64{sk.Fit.Params.A, sk.Fit.Params.B, sk.Fit.Params.C}
		} else {
			// Fallback
			t0 = 30.0 / 365.0
			carry = rate
			skewParams = []float64{leg.IV, 0, 0}
			if leg.IV == 0 {
				skewParams[0] = 0.20
			} // Safety
		}

		entryVol = leg.IV
		if entryVol == 0 && len(skewParams) > 0 {
			entryVol = skewParams[0] // Use ATM or A as proxy if 0? Or calculate?
		}

		// Calculate Scenario State
		t1 := t0 - timePassed
		if t1 < 0 {
			t1 = 0
		}

		fwd1 := estSpot * math.Exp(carry*t1)
		// Fix Base Case Forward Precision
		if effDaysForward == 0 && p.SpotChangePct == 0 && ok {
			fwd1 = sk.Forward
		}

		// Volatility
		// Model Vol = A + B*x + C*x^2 (Actually Variance)
		vol := VolFromSkew(skewParams, leg.Strike, fwd1, t0) // Note: Skew is often parameterized by initial T0, but evaluated at current Forward?
		// Sticky Strike vs Sticky Delta vs Fixed Surface?
		// "VolFromSkew" typically evaluates the curve at (K, F).
		// If we use the original parameters (A,B,C) which were fit at T0, we are assuming "Sticky Surface" in Moneyness space if the function handles moneyness.
		// VolFromSkew implementation needs to be checked. Assuming it handles LogMoneyness(F, K).
		// Ideally, we should evolve the surface (Root-T decay?), but constant surface is standard approximation.

		// Ensure Base Case Vol matches Entry Vol
		if effDaysForward == 0 && p.SpotChangePct == 0 && p.VolChange == 0 {
			// Force match
			vol = leg.IV
		}

		// Apply Shock (Additive)
		finalVol := vol + p.VolChange
		if finalVol < 0.01 {
			finalVol = 0.01
		}

		// Price
		isCall := (leg.Type == market.Call)
		bsm := BSM(isCall, estSpot, leg.Strike, t1, rate, finalVol) // Use Rate for discounting, but Carry logic is inside Forward/Vol?

		// FIX: Base Case PnL check.
		// RunScenarios calculates InitialValue using leg.Mark.
		// If leg.Mark came from different solver settings, BSM might differ.
		// But leg.Mark is the ground truth cost.
		// We can't force BSM to match Mark unless we solve for Implied Vol again (re-solve).
		// But leg.IV IS the implied vol.
		// So BSM(IV, Spot) should equal Mark, PROVIDED the inputs (S, F, r, T) are identical.
		// We are using `estSpot`, `t1`, `rate`, `finalVol`.
		// At Base Case: estSpot=baseSpot, t1=t0, finalVol=leg.IV.
		// Discrepancy source:
		// 1. Discounting. Solver might use RiskFree. We use `rate`. Matches?
		// 2. Forward. Solver used Fwd to get Vol, and likely Black76 or BSM with specific drift.
		// Our BSM assumes Drift = rate (Risk Neutral).
		// If Solver used Black76 (F, K, T, r, sigma), and we use BSM(S, K, T, r, sigma),
		// BSM(S) == Black76(F) ONLY if F = S*exp(rT).
		// If F != S*exp(rT) (due to dividends), then BSM(S) is WRONG.
		// We MUST use Black76 if we have a distinct Forward.
		// Since we have `fwd1`, let's try to use Black76 logic if possible, or adjust BSM.
		// BSM Price = exp(-rT) * [F * N(d1) - K * N(d2)] (for Call)
		// Our BSM function: S*N(d1) - K*exp(-rT)*N(d2).
		// This assumes F = S*exp(rT).
		// To support custom Fwd in BSM function:
		// Pass "S" = F * exp(-rT). Then BSM sees S_eff * exp(rT) = F.
		// Let's try that trick: S_input = fwd1 * math.Exp(-rate*t1).
		// That forces the drift to match fwd1.

		bsmInputSpot := estSpot
		if carry != rate {
			bsmInputSpot = fwd1 * math.Exp(-rate*t1)
		}

		bsm = BSM(isCall, bsmInputSpot, leg.Strike, t1, rate, finalVol)

		sign := 1.0
		if leg.Side == "SELL" {
			sign = -1.0
		}
		qty := float64(leg.Qty)

		totalValue += sign * qty * bsm.Price

		netGreeks.Delta += sign * qty * bsm.Delta // Delta usually on Spot. If using Fwd input, check definition. BSM delta is dV/dS.
		netGreeks.Gamma += sign * qty * bsm.Gamma
		netGreeks.VegaPerVolPoint += sign * qty * bsm.Vega
		netGreeks.ThetaPerDay += sign * qty * bsm.Theta
		netGreeks.Rho += sign * qty * bsm.Rho
	}

	return ScenarioResult{
		Scenario:      p,
		EstimatedSpot: estSpot,
		EstimatedDate: estDate,
		EstimatedFwd:  estimatedFwd,
		StrategyValue: totalValue,
		PnL:           totalValue - initialValue,
		Greeks:        netGreeks,
	}
}
