package replay

import (
	"fmt"
	"math"
	"strings"
	"time"

	"volatility-engine/internal/domain/market"
)

type SlippageModel struct {
	Mode     string  // "none" | "bps" | "ticks"
	Bps      float64 // premium bps
	Tick     float64 // absolute tick
	TickSize float64 // NSE tick size for rounding (e.g. 0.05)
}

type FeeModel struct {
	PerLeg   float64
	PerOrder float64
	Bps      float64
}

type ExecSim struct {
	Slip SlippageModel
	Fees FeeModel
}

func NewExecSim(s SlippageModel, f FeeModel) *ExecSim { return &ExecSim{Slip: s, Fees: f} }

// roundToTickDirectional rounds price to nearest tick size with adverse selection
func roundToTickDirectional(price, tick float64, side string) float64 {
	if tick <= 0 {
		return price
	}
	x := price / tick
	side = strings.ToUpper(strings.TrimSpace(side))
	if side == "BUY" {
		return math.Ceil(x) * tick // Buy fills at higher price (worse)
	}
	return math.Floor(x) * tick // Sell fills at lower price (worse)
}

func (x *ExecSim) FillOrder(asOf time.Time, snap *market.Snapshot, ord OrderIntent, multiplier float64) (Fill, error) {
	var legFills []LegFill
	net := 0.0
	totalNotional := 0.0

	for _, leg := range ord.Legs {
		q, found := findQuote(snap, leg.Strike, string(leg.Type), leg.Expiry)
		if !found {
			return Fill{}, fmt.Errorf("quote not found for leg %s %s %.0f", leg.Type, leg.Expiry, leg.Strike)
		}

		fillPx, basis, slipAmt, err := x.pickFillPrice(q, leg.Side)
		if err != nil {
			return Fill{}, err
		}

		// Normalize side string to prevent comparison bugs
		side := strings.ToUpper(strings.TrimSpace(leg.Side))

		legPoints := fillPx * float64(leg.Qty)
		totalNotional += legPoints // Sum of absolute fill amounts in points

		// Apply correct sign: SELL = credit (+), BUY = debit (-)
		switch side {
		case "SELL":
			net += +legPoints // credit
		case "BUY":
			net += -legPoints // debit
		default:
			return Fill{}, fmt.Errorf("invalid side: %q", leg.Side)
		}

		legFills = append(legFills, LegFill{
			Leg:          leg,
			FillPrice:    fillPx,
			FillBasis:    basis,
			SlippageMode: x.Slip.Mode,
			SlippageAmt:  slipAmt,
			TickSize:     x.Slip.TickSize,
		})
	}

	// Fee Calculation
	fee := 0.0

	// 1. Per Order Fee
	fee += x.Fees.PerOrder

	// 2. Per Leg Fee
	fee += x.Fees.PerLeg * float64(len(ord.Legs))

	// 3. Bps Fee (on Notional Value in INR)
	if x.Fees.Bps > 0 {
		// Notional in INR = Total Abs Points * Multiplier
		notionalINR := totalNotional * multiplier
		fee += notionalINR * (x.Fees.Bps / 10000.0)
	}

	return Fill{
		AsOf:       asOf,
		PositionID: ord.PositionID,
		StrategyID: ord.StrategyID,
		Action:     ord.Action,
		LegFills:   legFills,
		NetPremium: net, // Keep in POINTS
		FeeINR:     fee,
	}, nil
}

func (x *ExecSim) pickFillPrice(q market.Quote, side string) (float64, string, float64, error) {
	// 1) Normalize side immediately to prevent bugs
	side = strings.ToUpper(strings.TrimSpace(side))

	// 2) Spread check: reject if bid-ask spread is too wide (> 10% of mid)
	// ONLY if both bid and ask exist, to avoid false positives on missing legs
	if q.Market.Bid > 0 && q.Market.Ask > 0 && q.Market.Mid > 0 {
		spread := q.Market.Ask - q.Market.Bid
		if spread > 0.1*q.Market.Mid {
			return 0, "", 0, fmt.Errorf("spread too wide for reliable execution (%.2f vs mid %.2f)", spread, q.Market.Mid)
		}
	}

	// 3) Determine Base Price & Basis
	// BUY  -> ASK (fallback to Mid)
	// SELL -> BID (fallback to Mid)
	base := 0.0
	basis := ""
	if side == "BUY" {
		if q.Market.Ask > 0 {
			base = q.Market.Ask
			basis = "ASK"
		} else {
			base = q.Market.Mid
			basis = "MID"
		}
	} else { // SELL
		if q.Market.Bid > 0 {
			base = q.Market.Bid
			basis = "BID"
		} else {
			base = q.Market.Mid
			basis = "MID"
		}
	}

	// Safety: if base is still <= 0, we cannot fill
	if base <= 0 {
		return 0, "", 0, fmt.Errorf("no valid price to fill %s (Bid:%.2f Ask:%.2f Mid:%.2f)", side, q.Market.Bid, q.Market.Ask, q.Market.Mid)
	}

	// 4) Calculate Slippage Amount (always positive magnitude)
	slipAmt := 0.0
	switch x.Slip.Mode {
	case "bps":
		slipAmt = base * (x.Slip.Bps / 10000.0)
	case "ticks":
		slipAmt = x.Slip.Tick * x.Slip.TickSize
	}

	// 5) Apply Slippage (Adverse Direction)
	// BUY  -> Pay More (Add Slippage)
	// SELL -> Receive Less (Subtract Slippage)
	rawPrice := base
	if side == "BUY" {
		rawPrice += slipAmt
	} else {
		rawPrice -= slipAmt
	}

	// 6) Directional Rounding
	// BUY  -> Round UP to nearest tick
	// SELL -> Round DOWN to nearest tick
	finalPrice := roundToTickDirectional(rawPrice, x.Slip.TickSize, side)

	// Prevent negative price
	if finalPrice < 0 {
		finalPrice = 0
	}

	return finalPrice, basis, slipAmt, nil
}

func findQuote(snap *market.Snapshot, strike float64, optType string, expiry string) (market.Quote, bool) {
	for _, q := range snap.Quotes {
		if q.Contract.Strike == strike &&
			string(q.Contract.Type) == optType &&
			q.Contract.Expiry.Format("2006-01-02") == expiry {
			return q, true
		}
	}
	return market.Quote{}, false
}

// FillOrderAtIntrinsic fills an order using intrinsic value as a last resort
// when quote-based FillOrder fails (e.g. expired contracts, data gaps).
// Intrinsic: CALL = max(0, spot - strike), PUT = max(0, strike - spot)
// No slippage or spread checks — this is a fallback for force-close only.
func (x *ExecSim) FillOrderAtIntrinsic(asOf time.Time, spot float64, ord OrderIntent, multiplier float64) Fill {
	var legFills []LegFill
	net := 0.0
	totalNotional := 0.0

	for _, leg := range ord.Legs {
		// Calculate intrinsic value
		intrinsic := 0.0
		if string(leg.Type) == "CALL" {
			intrinsic = math.Max(0, spot-leg.Strike)
		} else { // PUT
			intrinsic = math.Max(0, leg.Strike-spot)
		}

		side := strings.ToUpper(strings.TrimSpace(leg.Side))
		legPoints := intrinsic * float64(leg.Qty)
		totalNotional += legPoints

		switch side {
		case "SELL":
			net += +legPoints
		case "BUY":
			net += -legPoints
		}

		legFills = append(legFills, LegFill{
			Leg:          leg,
			FillPrice:    intrinsic,
			FillBasis:    "INTRINSIC",
			SlippageMode: "none",
			SlippageAmt:  0,
			TickSize:     x.Slip.TickSize,
		})
	}

	// Fee Calculation (same logic as FillOrder)
	fee := x.Fees.PerOrder + x.Fees.PerLeg*float64(len(ord.Legs))
	if x.Fees.Bps > 0 {
		notionalINR := totalNotional * multiplier
		fee += notionalINR * (x.Fees.Bps / 10000.0)
	}

	return Fill{
		AsOf:       asOf,
		PositionID: ord.PositionID,
		StrategyID: ord.StrategyID,
		Action:     ord.Action,
		LegFills:   legFills,
		NetPremium: net,
		FeeINR:     fee,
	}
}
