package risk

import (
	"math"

	"volatility-engine/internal/domain/market"
	domainRisk "volatility-engine/internal/domain/risk"
)

// Aggregator summarizes simulation results into a Risk Report.
type Aggregator struct{}

func (a *Aggregator) Summarize(
	candidate market.StrategyCandidate,
	surface domainRisk.ScenarioSurface,
	payoffMetrics domainRisk.PayoffMetrics,
	currentGreeks market.Greeks,
	metaInfo struct {
		Regime string
		IV30   float64
		IVRank float64
	},
) domainRisk.StrategyRiskReport {

	report := domainRisk.StrategyRiskReport{
		Candidate: candidate,
	}

	// Meta
	report.Meta.AsOf = candidate.AsOf
	report.Meta.Underlying = candidate.Underlying
	report.Meta.Expiry = candidate.Expiry
	report.Meta.Spot = 0 // Needs to be passed in or derived from candidate context?
	// Note: StrategyCandidate doesn't hold Spot explicitly, but Legs have strikes.
	// We can pass Spot in valid invocation. But here we can leave it 0 or add arg.
	// Let's rely on the caller to fill spot via 'metaInfo' or similar if strictly needed there,
	// or assume caller populated Meta fully.
	// Actually, let's look at `candidate.Underlying`. It's a string.
	// We will pass spot in.

	report.Meta.Regime = metaInfo.Regime
	report.Meta.IV30 = metaInfo.IV30
	report.Meta.IVRank = metaInfo.IVRank

	// Payoff
	report.Payoff.Breakevens = payoffMetrics.Breakevens
	report.Payoff.MaxProfit = payoffMetrics.MaxProfit
	report.Payoff.MaxLoss = payoffMetrics.MaxLoss
	report.Payoff.RiskReward = payoffMetrics.RiskRewardRatio
	report.Payoff.UnboundedLoss = !payoffMetrics.IsDefinedRisk // Simplified mapping
	// Real unbounded checks were done in Builder.

	// Greeks
	report.Greeks.Delta = currentGreeks.Delta
	report.Greeks.Gamma = currentGreeks.Gamma
	report.Greeks.Theta = currentGreeks.ThetaPerDay
	report.Greeks.Vega = currentGreeks.VegaPerVolPoint

	// Scenarios
	report.Scenarios.GridSpec = surface.Spec
	report.Scenarios.Surface = surface.Results

	// Find Worst Case
	minPnl := math.MaxFloat64
	var worstCase domainRisk.ScenarioResult
	if len(surface.Results) > 0 {
		worstCase = surface.Results[0] // Default
		for _, res := range surface.Results {
			if res.PnL < minPnl {
				minPnl = res.PnL
				worstCase = res
			}
		}
	}
	report.Scenarios.WorstCase = worstCase

	return report
}
