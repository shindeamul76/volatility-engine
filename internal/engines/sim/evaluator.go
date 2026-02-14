package sim

import (
	"math"
	"time"

	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/domain/risk"
	"volatility-engine/internal/quant/pricing"
)

// Evaluator runs the simulation grid for a strategy candidate.
type Evaluator struct {
	Pricer Pricer
}

// NewEvaluator creates a new evaluator.
func NewEvaluator() *Evaluator {
	return &Evaluator{
		Pricer: CachedPricer{Cache: NewResultCache()}, // Use caching by default
	}
}

// Evaluate computes the scenario surface for a candidate.
func (e *Evaluator) Evaluate(
	candidate market.StrategyCandidate,
	grid risk.ScenarioGridSpec,
	marketSnapshot market.Snapshot,
	r float64,
) risk.ScenarioSurface {

	results := make([]risk.ScenarioResult, 0, grid.ScenariosCount)
	spot := marketSnapshot.Underlying.Spot
	if spot == 0 {
		return risk.ScenarioSurface{Spec: grid}
	}

	layout := "2006-01-02"
	expiry, _ := time.Parse(layout, candidate.Expiry)
	daysToExpiry := expiry.Sub(marketSnapshot.AsOf).Hours() / 24.0
	tteYears0 := daysToExpiry / 365.0

	for _, dayStep := range grid.TimeSteps {
		daysForward := float64(dayStep)

		// New TTE
		newTTE := math.Max(0.00001, tteYears0-(daysForward/365.0))

		for _, spotShock := range grid.SpotShocks {
			newSpot := spot * (1 + spotShock)

			// Forward at Scenario
			fwdScn := newSpot * math.Exp(r*newTTE)

			for _, volShockPoints := range grid.VolShocks {
				// Vol Shock (in points, e.g. 5 = 0.05)
				volShock := volShockPoints / 100.0

				res := e.evaluatePoint(candidate, newSpot, fwdScn, newTTE, volShock, r)

				res.Scenario = risk.ScenarioPoint{
					SpotChangePct: spotShock,
					VolChange:     volShockPoints,
					DaysForward:   dayStep,
				}
				res.EstimatedSpot = newSpot
				// res.EstimatedDate = ...

				results = append(results, res)
			}
		}
	}

	return risk.ScenarioSurface{
		Spec:    grid,
		Results: results,
	}
}

func (e *Evaluator) evaluatePoint(
	candidate market.StrategyCandidate,
	spot float64,
	fwd float64, // Forward price at scenario time
	tte float64,
	volShift float64,
	r float64,
) risk.ScenarioResult {

	totalValue := 0.0
	netGreeks := market.Greeks{}

	for _, leg := range candidate.Legs {
		// Vol Logic:
		// Base Sigma (leg.IV) + Shock
		// Assuming leg.IV is decimal (0.20)
		baseSigma := leg.IV
		if baseSigma > 1.0 && baseSigma < 100 {
			// Heuristic fix if IV is percentage
			baseSigma = baseSigma / 100.0
		}

		newVol := math.Max(0.0001, baseSigma+volShift)

		ctx := pricing.PricingContext{
			Type:           leg.Type,
			S:              fwd, // Use Forward
			K:              leg.Strike,
			T:              tte,
			R:              r,
			Q:              0,
			Sigma:          newVol,
			IsForwardModel: true, // Key: Forward Model
		}

		pxRes := e.Pricer.Price(ctx)

		legValue := pxRes.Price * float64(leg.Qty)
		totalValue += legValue

		netGreeks.Delta += pxRes.Delta * float64(leg.Qty)
		netGreeks.Gamma += pxRes.Gamma * float64(leg.Qty)
		netGreeks.VegaPerVolPoint += pxRes.VegaPerVolPoint * float64(leg.Qty)
		netGreeks.ThetaPerDay += pxRes.ThetaPerDay * float64(leg.Qty)
	}

	initialCashFlow := 0.0
	if candidate.Entry.PremiumType == "CREDIT" {
		initialCashFlow = candidate.Entry.NetPremium
	} else if candidate.Entry.PremiumType == "DEBIT" {
		initialCashFlow = -candidate.Entry.NetPremium
	}

	pnl := totalValue + initialCashFlow

	pnlPct := 0.0
	costBasis := math.Abs(initialCashFlow)
	if costBasis > 0.01 {
		pnlPct = pnl / costBasis
	}

	return risk.ScenarioResult{
		StrategyValue: totalValue,
		PnL:           pnl,
		PnLPct:        pnlPct, // This might be massive if cost basis is near zero (credit strategies)
		Greeks:        netGreeks,
	}
}
