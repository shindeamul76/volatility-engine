package risk

import (
	"math"
	"sort"
	"volatility-engine/internal/domain/market"
)

// PayoffLeg represents a simplified option leg for payoff analysis.
type PayoffLeg struct {
	Quantity   float64 // Signed quantity: + for Buy, - for Sell
	Strike     float64
	Type       market.OptionType
	EntryPrice float64 // Price per unit at entry (e.g. premium paid/received)
}

// ConvertStrategyLegsToPayoffLegs helper
func ConvertStrategyLegsToPayoffLegs(legs []market.StrategyLeg) []PayoffLeg {
	var pl []PayoffLeg
	for _, l := range legs {
		q := float64(l.Qty)
		if l.Side == "SELL" {
			q = -q
		}
		pl = append(pl, PayoffLeg{
			Quantity:   q,
			Strike:     l.Strike,
			Type:       l.Type,
			EntryPrice: l.Mark, // Using current mark as entry for "what if" or actual entry price
		})
	}
	return pl
}

// CalculatePayoffAtSpot computes the net P&L at a specific spot price at expiry.
// PnL = Payoff - Cost
func CalculatePayoffAtSpot(legs []PayoffLeg, spot float64) float64 {
	totalValue := 0.0
	totalCost := 0.0

	for _, leg := range legs {
		// Intrinsic value
		intrinsic := 0.0
		if leg.Type == market.Call {
			if spot > leg.Strike {
				intrinsic = spot - leg.Strike
			}
		} else {
			if spot < leg.Strike {
				intrinsic = leg.Strike - spot
			}
		}

		totalValue += leg.Quantity * intrinsic
		totalCost += leg.Quantity * leg.EntryPrice
	}

	return totalValue - totalCost
}

// AnalyzePayoff constructs the full payoff profile and finding key metrics.
func AnalyzePayoff(legs []market.StrategyLeg) PayoffMetrics {
	plegs := ConvertStrategyLegsToPayoffLegs(legs)

	// 1. Identify all kinks (strikes)
	uniqueStrikes := make(map[float64]bool)
	for _, l := range plegs {
		uniqueStrikes[l.Strike] = true
	}
	var strikes []float64
	for k := range uniqueStrikes {
		strikes = append(strikes, k)
	}
	sort.Float64s(strikes)

	// 2. Evaluate at critical points:
	//    - large downside (0 or near 0)
	//    - strikes
	//    - large upside
	//    Also check slopes for unlimited risk.

	netCost := 0.0
	for _, l := range plegs {
		netCost += l.Quantity * l.EntryPrice
	}

	// Determine slopes at tails
	// Left tail (S -> 0): Puts are active (Quantity * (K - S)) => Slope = -Quantity. Calls are 0.
	// Right tail (S -> inf): Calls are active (Quantity * (S - K)) => Slope = +Quantity. Puts are 0.

	leftSlope := 0.0
	rightSlope := 0.0

	for _, l := range plegs {
		if l.Type == market.Put {
			leftSlope += l.Quantity * (-1) // Put intrinsic is K-S, derivative wrt S is -1
		}
		if l.Type == market.Call {
			rightSlope += l.Quantity * (1) // Call intrinsic is S-K, derivative wrt S is 1
		}
	}

	unboundedLoss := false
	unboundedProfit := false

	// If you are net long calls (slope > 0), you have unlimited profit on right.
	// If you are net short calls (slope < 0), you have unlimited loss on right.
	if rightSlope > 0 {
		unboundedProfit = true
	}
	if rightSlope < 0 {
		unboundedLoss = true
	}

	// Left tail:
	// Net long puts (slope < 0 ? No, slope is wrt S. Lower S means higher profit.)
	// Wait.
	// Value = Sum(Qty * (K - S)) = Sum(Qty*K) - S * Sum(Qty)
	// Slope w.r.t S is -Sum(Qty_puts).
	// As S decreases, Value increases if Sum(Qty_puts) > 0.
	// But S cannot go below 0. So technically limited by S=0.
	// However, for practical purposes, if slope is significantly negative, it's "large risk" or "unlimited" in models that allow S<0.
	// But strictly, max loss on puts is limited by S=0.
	// "Unlimited" usually refers to short calls or short straddles/strangles.
	// Let's stick to standard convention:
	// Short Put: S goes to 0 -> Loss is K * Qty. It is finite but large.
	// Usually we flag it as "defined risk" only if it's capped.
	// We will mark unboundedLoss if right tail is short or left tail is short (though left is bounded by 0).
	// Actually, strict definition:
	// Unbounded UP: rightSlope > 0.
	// Unbounded DOWN: rightSlope < 0 (infinity loss).
	// Left side strictly bounded by 0. So we check value at 0.

	// 3. Calculate Breakevens
	// Payoff function is piecewise linear.
	// We iterate through intervals (-inf, K1), (K1, K2), ... (Kn, inf)
	// In each interval, Value(S) = Intercept + Slope * S
	// We check if crossing 0 occurs.

	breakevens := []float64{}
	points := []PayoffPoint{}

	// Test points: 0, Strikes, High Point
	testPoints := []float64{}
	if len(strikes) > 0 {
		testPoints = append(testPoints, 0.0) // Lower bound
		testPoints = append(testPoints, strikes...)
		testPoints = append(testPoints, strikes[len(strikes)-1]*2.0) // Upper bound arbitrary
	} else {
		// No strikes? weird leg set.
		testPoints = append(testPoints, 0.0, 10000.0)
	}

	maxProfit := -math.MaxFloat64
	maxLoss := math.MaxFloat64 // PnL is signed, so looking for min value

	// Evaluate at all strikes to find min/max
	for _, s := range testPoints {
		pnl := CalculatePayoffAtSpot(plegs, s)
		points = append(points, PayoffPoint{Spot: s, Profit: pnl})
		if pnl > maxProfit {
			maxProfit = pnl
		}
		if pnl < maxLoss {
			maxLoss = pnl
		}
	}

	// Logic to find exact breakevens between strikes
	// We need the slope and intercept for each segment.
	// Or simpler: Linear interpolation between computed points at strikes.
	// Since it's linear between strikes, if sign changes, we find root.

	// Sorted points by Spot
	// (Strikes are sorted, test points roughly too, but let's be careful)
	// We just iterate through the sorted intervals defined by strikes.

	// Interval 0: [0, K1]
	// Interval i: [Ki, K(i+1)]
	// Interval n: [Kn, infinity]

	// Helper to solve linear: P1 at S1, P2 at S2. Find S where P=0.
	// P(S) = P1 + (S - S1) * (P2 - P1) / (S2 - S1)
	// 0 = P1 + (S - S1) * M  => -P1/M = S - S1 => S = S1 - P1/M

	prevSpot := 0.0
	prevPnL := CalculatePayoffAtSpot(plegs, 0.0)

	// Search intervals up to max strike
	for _, k := range strikes {
		currSpot := k
		currPnL := CalculatePayoffAtSpot(plegs, k)

		if (prevPnL > 0 && currPnL < 0) || (prevPnL < 0 && currPnL > 0) {
			// Crosses zero
			slope := (currPnL - prevPnL) / (currSpot - prevSpot)
			if slope != 0 {
				be := prevSpot - prevPnL/slope
				breakevens = append(breakevens, be)
			}
		} else if currPnL == 0 {
			// Exact hit
			breakevens = append(breakevens, currSpot)
		}

		prevSpot = currSpot
		prevPnL = currPnL
	}

	// Check last segment: [Kn, infinity]
	// Use a proxy point far out
	farSpot := strikes[len(strikes)-1] + 1000.0
	farPnL := CalculatePayoffAtSpot(plegs, farSpot)

	if (prevPnL > 0 && farPnL < 0) || (prevPnL < 0 && farPnL > 0) {
		slope := (farPnL - prevPnL) / (farSpot - prevSpot)
		if slope != 0 {
			be := prevSpot - prevPnL/slope
			breakevens = append(breakevens, be)
		}
	}

	// Clean up breakevens (deduplicate, round)
	bps := []float64{}
	seen := make(map[int]bool)
	for _, be := range breakevens {
		// Round to 2 decimals for dup check
		key := int(be * 100)
		if !seen[key] {
			bps = append(bps, be)
			seen[key] = true
		}
	}

	// Finalize Unlimited flags
	if rightSlope < 0 {
		maxLoss = math.Inf(-1)
	}
	if rightSlope > 0 {
		maxProfit = math.Inf(1)
	}
	// Left side check for naked puts
	if leftSlope > 0 { // Net Short Puts -> Slope wrt S is positive (Value goes down as S goes down)
		// Wait, Check carefully.
		// Put Payoff = max(0, K-S).
		// Short Put Payoff = -max(0, K-S).
		// As S -> 0, Payoff -> -K. Finite.
		// So strictly, Max Loss is finite for puts.
	}

	// One check: If we have unbounded loss on right, maxLoss is -Inf.
	// If unbounded profit on right, maxProfit is +Inf.

	return PayoffMetrics{
		EntryCost:       netCost,
		MaxProfit:       maxProfit,
		MaxLoss:         maxLoss,
		UnboundedProfit: unboundedProfit,
		UnboundedLoss:   unboundedLoss,
		Breakevens:      bps,
		RiskRewardRatio: calculateRR(maxProfit, maxLoss),
		ProfilePoints:   points,
	}
}

func calculateRR(profit, loss float64) float64 {
	if math.IsInf(loss, -1) {
		return 0 // Infinite risk
	}
	lossAbs := math.Abs(loss)
	if lossAbs < 0.001 {
		return 0 // No risk?
	}
	if math.IsInf(profit, 1) {
		return 999.0 // Infinite reward
	}
	return profit / lossAbs
}
