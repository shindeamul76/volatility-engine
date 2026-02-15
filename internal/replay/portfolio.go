package replay

import (
	"fmt"
	"time"

	"volatility-engine/internal/domain/market"
)

type Portfolio struct {
	InitialCash        float64
	Cash               float64
	ContractMultiplier float64 // e.g. 65 for NIFTY
	Positions          map[string]*Position
	NextID             int

	// Realized Stats
	RealizedPnL float64
	Wins        int
	Losses      int
	TotalTrades int

	// Day tracking (for RiskGate circuit breakers)
	DayStartEquity float64
}

type Position struct {
	ID         string
	StrategyID string
	OpenedAt   time.Time
	ClosedAt   *time.Time

	EntryPremium float64 // INR (credit +, debit -)
	EntryFee     float64 // INR
	ExitPremium  float64 // INR (credit +, debit -)
	ExitFee      float64 // INR

	RealizedPnL     float64 // (Entry + Exit) - (EntryFee + ExitFee)
	MaxLossEstimate float64 // Set by RiskGate at entry for risk tracking (INR, always positive)
	LegFills        []LegFill
}

func NewPortfolio(initialCash float64, multiplier float64) *Portfolio {
	if multiplier <= 0 {
		multiplier = 1.0
	}
	return &Portfolio{
		InitialCash:        initialCash,
		Cash:               initialCash,
		ContractMultiplier: multiplier,
		Positions:          map[string]*Position{},
		NextID:             1,
	}
}

func (p *Portfolio) NewPositionID() string {
	id := fmt.Sprintf("pos_%06d", p.NextID)
	p.NextID++
	return id
}

func (p *Portfolio) ApplyFill(fill Fill) {
	switch fill.Action {
	case ActionOpen:
		pos := &Position{
			ID:           fill.PositionID,
			StrategyID:   fill.StrategyID,
			OpenedAt:     fill.AsOf,
			EntryPremium: RoundINR(fill.NetPremium * p.ContractMultiplier), // Convert points to INR, rounded
			EntryFee:     RoundINR(fill.FeeINR),
			LegFills:     fill.LegFills,
		}
		p.Positions[pos.ID] = pos

		// Cash Change = Premium - Fee
		// If NetPremium is negative (Debit), Cash decreases by (Premium + Fee)
		// If positive (Credit), Cash increases by (Premium - Fee)
		p.Cash = RoundINR(p.Cash + (pos.EntryPremium - pos.EntryFee))

		p.TotalTrades++
	case ActionClose:
		pos := p.Positions[fill.PositionID]
		if pos == nil || pos.ClosedAt != nil {
			return
		}

		pos.ExitPremium = RoundINR(fill.NetPremium * p.ContractMultiplier)
		pos.ExitFee = RoundINR(fill.FeeINR)
		t := fill.AsOf
		pos.ClosedAt = &t

		// Cash Change = Premium - Fee
		p.Cash = RoundINR(p.Cash + (pos.ExitPremium - pos.ExitFee))

		// Realized PnL = (Entry + Exit) - Total Fees
		// Entry + Exit is gross PnL
		grossPnL := pos.EntryPremium + pos.ExitPremium
		totalFees := pos.EntryFee + pos.ExitFee
		pos.RealizedPnL = RoundINR(grossPnL - totalFees)

		p.RealizedPnL = RoundINR(p.RealizedPnL + pos.RealizedPnL)
		if pos.RealizedPnL > 0 {
			p.Wins++
		} else if pos.RealizedPnL < 0 {
			p.Losses++
		}
	}
}

func (p *Portfolio) OpenPositions() []*Position {
	var out []*Position
	for _, pos := range p.Positions {
		if pos.ClosedAt == nil {
			out = append(out, pos)
		}
	}
	return out
}

// StartOfDay records the equity at the start of each snapshot day for daily PnL tracking.
func (p *Portfolio) StartOfDay(equity float64) {
	p.DayStartEquity = equity
}

// TotalOpenRisk returns the sum of MaxLossEstimate for all open positions (INR).
func (p *Portfolio) TotalOpenRisk() float64 {
	total := 0.0
	for _, pos := range p.OpenPositions() {
		total += pos.MaxLossEstimate
	}
	return total
}

type MTMResult struct {
	PosID             string
	LiquidationValue  float64 // Strict Bid/Ask
	LiquidationPoints float64
	MidValue          float64 // Mid Price
	MidPoints         float64
	UnrealizedPnL     float64 // Based on Liquidation Value
	CostToClose       float64
}

// MarkToMarket calculates the current value of positions.
// Returns individual results and Total Liquidation Equity.
func (p *Portfolio) MarkToMarket(snap *market.Snapshot) ([]MTMResult, float64, float64) {
	var results []MTMResult
	totalLiquidationEquity := p.Cash
	totalMidEquity := p.Cash

	for _, pos := range p.OpenPositions() {
		liqPoints := 0.0
		midPoints := 0.0

		for _, legFill := range pos.LegFills {
			q, ok := findQuoteForLeg(snap, legFill.Leg)
			if !ok {
				continue
			}

			// 1. Strict Liquidation Price
			pxLiq := getLiquidationPrice(q, legFill.Leg)

			// 2. Mid Price (Analytics)
			pxMid := q.Market.Mid

			qty := float64(legFill.Leg.Qty)

			sign := 1.0
			if legFill.Leg.Side == "SELL" {
				sign = -1.0
			}
			liqPoints += sign * pxLiq * qty
			midPoints += sign * pxMid * qty
		}

		liqValueINR := liqPoints * p.ContractMultiplier
		midValueINR := midPoints * p.ContractMultiplier

		// UnrealizedPnL = liquidation value (what we get if we close now) - entry cost (what we paid)
		unrealPnL := liqValueINR + pos.EntryPremium // Correct: EntryPremium is signed

		costToClose := -liqValueINR

		results = append(results, MTMResult{
			PosID:             pos.ID,
			LiquidationValue:  liqValueINR,
			LiquidationPoints: liqPoints,
			MidValue:          midValueINR,
			MidPoints:         midPoints,
			UnrealizedPnL:     unrealPnL,
			CostToClose:       costToClose,
		})

		totalLiquidationEquity += liqValueINR
		totalMidEquity += midValueINR
	}

	return results, totalLiquidationEquity, totalMidEquity
}

func getLiquidationPrice(q market.Quote, leg ReplayLeg) float64 {
	// If the position leg is BUY (long), closing is SELL => use BID
	if leg.Side == "BUY" {
		// Strict: If Bid is missing (0), value is 0.
		return q.Market.Bid
	}

	// If the position leg is SELL (short), closing is BUY => use ASK
	if leg.Side == "SELL" {
		if q.Market.Ask > 0 {
			return q.Market.Ask
		}
		// Fallback for Shorts: If Ask is missing (illiquid/deep ITM?),
		// we cannot say it's worth 0 (that would be infinite profit!).
		// We use Mid as a proxy, or maybe Mid + penalty.
		// For now, use Mid to avoid blowing up PnL artomatically, but this is a data gap.
		return q.Market.Mid
	}

	return 0.0
}

func findQuoteForLeg(snap *market.Snapshot, leg ReplayLeg) (market.Quote, bool) {
	for _, q := range snap.Quotes {
		if q.Contract.Strike == leg.Strike &&
			q.Contract.Type == leg.Type &&
			q.Contract.Expiry.Format("2006-01-02") == leg.Expiry {
			return q, true
		}
	}
	// Fallback if not found (expired? data error?) -> 0 price
	return market.Quote{}, false
}
