package payoff

import (
	"math"

	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/domain/risk"
)

// Builder constructs payoff diagrams.
type Builder struct{}

// BuildPayoff generates the P&L curve at expiry.
func (b *Builder) BuildPayoff(candidate market.StrategyCandidate, spot float64) risk.PayoffCurve {
	// Define Range
	minStrike := math.MaxFloat64
	maxStrike := 0.0

	for _, leg := range candidate.Legs {
		if leg.Strike < minStrike {
			minStrike = leg.Strike
		}
		if leg.Strike > maxStrike {
			maxStrike = leg.Strike
		}
	}

	if minStrike == math.MaxFloat64 {
		minStrike = spot * 0.9
		maxStrike = spot * 1.1
	}

	// Grid: [0.5 * minStrike, 1.5 * maxStrike]
	low := minStrike * 0.5
	high := maxStrike * 1.5
	if low > spot*0.8 {
		low = spot * 0.8
	} // Ensure we cover enough downside
	if high < spot*1.2 {
		high = spot * 1.2
	} // Ensure we cover enough upside

	steps := 200 // More granular
	stepSize := (high - low) / float64(steps)

	points := make([]risk.PayoffPoint, 0, steps+1)

	initialCashFlow := 0.0
	if candidate.Entry.PremiumType == "CREDIT" {
		initialCashFlow = candidate.Entry.NetPremium
	} else if candidate.Entry.PremiumType == "DEBIT" {
		initialCashFlow = -candidate.Entry.NetPremium
	}

	for i := 0; i <= steps; i++ {
		s := low + float64(i)*stepSize

		terminalValue := 0.0
		for _, leg := range candidate.Legs {
			intrinsic := 0.0
			if leg.Type == market.Call {
				intrinsic = math.Max(0, s-leg.Strike)
			} else {
				intrinsic = math.Max(0, leg.Strike-s)
			}
			terminalValue += intrinsic * float64(leg.Qty)
		}

		pnl := terminalValue + initialCashFlow
		points = append(points, risk.PayoffPoint{
			SpotPrice: s,
			Profit:    pnl,
		})
	}

	return risk.PayoffCurve{Points: points}
}

// CalculateMetrics computes Max Profit, Loss, Breakevens from the curve.
func (b *Builder) CalculateMetrics(curve risk.PayoffCurve) risk.PayoffMetrics {
	if len(curve.Points) == 0 {
		return risk.PayoffMetrics{}
	}

	maxProfit := -math.MaxFloat64
	maxLoss := math.MaxFloat64

	// Track min/max
	for _, p := range curve.Points {
		if p.Profit > maxProfit {
			maxProfit = p.Profit
		}
		if p.Profit < maxLoss {
			maxLoss = p.Profit
		}
	}

	breakevens := []float64{}
	var isDefined bool = true

	// Analyze curve
	for i, p := range curve.Points {
		if i > 0 {
			prev := curve.Points[i-1]
			// Zero crossing
			if (prev.Profit < 0 && p.Profit >= 0) || (prev.Profit > 0 && p.Profit <= 0) {
				slope := (p.Profit - prev.Profit) / (p.SpotPrice - prev.SpotPrice)
				if math.Abs(slope) > 1e-9 {
					bx := prev.SpotPrice - prev.Profit/slope
					breakevens = append(breakevens, bx)
				}
			}
		}
	}

	rr := 0.0
	if math.Abs(maxLoss) > 0 {
		rr = maxProfit / math.Abs(maxLoss)
	}

	// Unbounded check using slope at edges
	if len(curve.Points) > 2 {
		startSlope := (curve.Points[1].Profit - curve.Points[0].Profit) / (curve.Points[1].SpotPrice - curve.Points[0].SpotPrice)
		endSlope := (curve.Points[len(curve.Points)-1].Profit - curve.Points[len(curve.Points)-2].Profit) /
			(curve.Points[len(curve.Points)-1].SpotPrice - curve.Points[len(curve.Points)-2].SpotPrice)

		// Heuristic: If slope is significant (indicating loss grows as price moves away), it's undefined.
		// Start (Low Price): If P decreases as S decreases (slope > 0).
		// End (High Price): If P decreases as S increases (slope < 0).
		if startSlope > 0.01 || endSlope < -0.01 {
			isDefined = false
		}
	}

	return risk.PayoffMetrics{
		MaxProfit:       maxProfit,
		MaxLoss:         maxLoss,
		RiskRewardRatio: rr,
		Breakevens:      breakevens,
		IsDefinedRisk:   isDefined,
	}
}
