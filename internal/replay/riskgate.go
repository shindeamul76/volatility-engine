package replay

import (
	"fmt"
	"math"

	"volatility-engine/internal/config"
	"volatility-engine/internal/domain/market"
)

// RiskVerdict is the output of a RiskGate evaluation.
type RiskVerdict struct {
	Action  string   // "APPROVED", "REJECTED", "RESIZED"
	Qty     int      // Final lot count
	Reasons []string // Audit trail

	// Sizing details (for audit logging)
	BudgetINR    float64
	RiskPerLot   float64
	OriginalQty  int
	EntryCostINR float64
	MaxLossINR   float64
}

// RiskGate evaluates proposed entries against risk limits and computes position sizes.
type RiskGate struct {
	Cfg config.RiskGateConfig
}

// NewRiskGate creates a RiskGate from config.
func NewRiskGate(cfg config.RiskGateConfig) *RiskGate {
	return &RiskGate{Cfg: cfg}
}

// Evaluate checks risk limits, computes qty, and returns a verdict.
// candidate: the strategy candidate proposed by the Decider
// pf: current portfolio state
// equity: current liquidation equity
// peakEquity: historical peak equity for drawdown calc
// multiplier: contract multiplier (e.g. 65 for NIFTY)
func (rg *RiskGate) Evaluate(
	candidate market.StrategyCandidate,
	pf *Portfolio,
	equity float64,
	peakEquity float64,
	multiplier float64,
) RiskVerdict {
	if !rg.Cfg.Enabled {
		// Pass-through: return original qty unchanged
		qty := 1
		if len(candidate.Legs) > 0 {
			qty = candidate.Legs[0].Qty
		}
		return RiskVerdict{
			Action:      "APPROVED",
			Qty:         qty,
			Reasons:     []string{"RiskGate disabled, pass-through"},
			OriginalQty: qty,
		}
	}

	var reasons []string

	// ──── 1. Portfolio Circuit Breakers ────

	// Daily loss stop
	dailyPnL := equity - pf.DayStartEquity
	if pf.DayStartEquity > 0 && dailyPnL <= -rg.Cfg.MaxDailyLossINR {
		return RiskVerdict{
			Action:  "REJECTED",
			Qty:     0,
			Reasons: []string{fmt.Sprintf("Daily loss stop: daily PnL %.2f <= -%.2f", dailyPnL, rg.Cfg.MaxDailyLossINR)},
		}
	}

	// Drawdown stop
	if peakEquity > 0 {
		drawdownPct := (equity - peakEquity) / peakEquity
		if drawdownPct <= -rg.Cfg.MaxDrawdownPct {
			return RiskVerdict{
				Action:  "REJECTED",
				Qty:     0,
				Reasons: []string{fmt.Sprintf("Drawdown stop: %.2f%% <= -%.2f%%", drawdownPct*100, rg.Cfg.MaxDrawdownPct*100)},
			}
		}
	}

	// ──── 2. Trade-Level Cost Check (for 1 lot) ────

	// Determine entry cost per lot and max loss per lot
	entryCostPerLot := math.Abs(candidate.Entry.NetPremium) * multiplier
	maxLossPerLot := candidate.Metrics.MaxLossApprox * multiplier
	marginPerLot := candidate.Metrics.MarginReq * multiplier

	isDebit := candidate.Entry.PremiumType == "DEBIT"

	if isDebit {
		// Long premium: risk = debit paid
		if maxLossPerLot <= 0 {
			maxLossPerLot = entryCostPerLot
		}
		if marginPerLot <= 0 {
			marginPerLot = entryCostPerLot
		}
	} else {
		// Defined risk credit: use MaxLossApprox from candidate metrics
		if maxLossPerLot <= 0 {
			// Fallback: use entry cost as proxy (conservative)
			maxLossPerLot = entryCostPerLot
		}
		if marginPerLot <= 0 {
			marginPerLot = maxLossPerLot
		}
	}

	// Check max debit per trade (for debit strategies)
	if isDebit && entryCostPerLot > rg.Cfg.MaxDebitPerTradeINR {
		return RiskVerdict{
			Action:       "REJECTED",
			Qty:          0,
			Reasons:      []string{fmt.Sprintf("Max debit exceeded: %.2f > %.2f", entryCostPerLot, rg.Cfg.MaxDebitPerTradeINR)},
			EntryCostINR: entryCostPerLot,
			MaxLossINR:   maxLossPerLot,
		}
	}

	// Check max loss per trade
	if maxLossPerLot > rg.Cfg.MaxLossPerTradeINR {
		return RiskVerdict{
			Action:       "REJECTED",
			Qty:          0,
			Reasons:      []string{fmt.Sprintf("Max loss per trade exceeded: %.2f > %.2f", maxLossPerLot, rg.Cfg.MaxLossPerTradeINR)},
			EntryCostINR: entryCostPerLot,
			MaxLossINR:   maxLossPerLot,
		}
	}

	// ──── 3. Position Sizing ────

	originalQty := 1
	if len(candidate.Legs) > 0 {
		originalQty = candidate.Legs[0].Qty
	}

	// Capital required per lot for sizing (Margin for credit, Premium for debit)
	capitalPerLot := marginPerLot
	if capitalPerLot <= 0 {
		capitalPerLot = entryCostPerLot
	}
	if capitalPerLot <= 0 {
		return RiskVerdict{
			Action:  "REJECTED",
			Qty:     0,
			Reasons: []string{"Cannot size: capital req per lot is zero or negative"},
		}
	}

	// Budget = equity × risk_per_trade_pct
	budgetINR := equity * rg.Cfg.RiskPerTradePct
	reasons = append(reasons, fmt.Sprintf("Budget: %.2f (%.1f%% of equity %.2f)", budgetINR, rg.Cfg.RiskPerTradePct*100, equity))

	// Base qty from budget
	qty := int(math.Floor(budgetINR / capitalPerLot))
	reasons = append(reasons, fmt.Sprintf("Raw qty: %d (budget %.2f / capital_per_lot %.2f)", qty, budgetINR, capitalPerLot))

	// Clamp to MaxLotsPerTrade
	if qty > rg.Cfg.MaxLotsPerTrade {
		qty = rg.Cfg.MaxLotsPerTrade
		reasons = append(reasons, fmt.Sprintf("Clamped to max lots: %d", rg.Cfg.MaxLotsPerTrade))
	}

	// Clamp to keep total open risk within MaxTotalRiskINR
	currentOpenRisk := pf.TotalOpenRisk()
	remainingRiskBudget := rg.Cfg.MaxTotalRiskINR - currentOpenRisk
	if remainingRiskBudget <= 0 {
		return RiskVerdict{
			Action:  "REJECTED",
			Qty:     0,
			Reasons: []string{fmt.Sprintf("Total risk cap reached: open risk %.2f >= max %.2f", currentOpenRisk, rg.Cfg.MaxTotalRiskINR)},
		}
	}

	maxQtyByRisk := int(math.Floor(remainingRiskBudget / maxLossPerLot))
	if qty > maxQtyByRisk {
		qty = maxQtyByRisk
		reasons = append(reasons, fmt.Sprintf("Clamped by total risk cap: %d (remaining budget %.2f)", qty, remainingRiskBudget))
	}

	// Final check: must have at least 1 lot
	if qty < 1 {
		return RiskVerdict{
			Action:       "REJECTED",
			Qty:          0,
			Reasons:      []string{fmt.Sprintf("Insufficient capital for 1 lot (Cost %.2f > Alloc %.2f)", capitalPerLot, budgetINR)},
			BudgetINR:    budgetINR,
			RiskPerLot:   capitalPerLot,
			EntryCostINR: entryCostPerLot,
			MaxLossINR:   maxLossPerLot,
			OriginalQty:  originalQty,
		}
	}

	// Determine action
	action := "APPROVED"
	if qty != originalQty {
		action = "RESIZED"
		reasons = append(reasons, fmt.Sprintf("Resized from %d to %d lots", originalQty, qty))
	}

	return RiskVerdict{
		Action:       action,
		Qty:          qty,
		Reasons:      reasons,
		BudgetINR:    budgetINR,
		RiskPerLot:   capitalPerLot,
		OriginalQty:  originalQty,
		EntryCostINR: entryCostPerLot * float64(qty),
		MaxLossINR:   maxLossPerLot * float64(qty),
	}
}

// ApplyQtyToLegs updates all legs in a set of ReplayLegs with the new quantity.
func ApplyQtyToLegs(legs []ReplayLeg, qty int) []ReplayLeg {
	out := make([]ReplayLeg, len(legs))
	copy(out, legs)
	for i := range out {
		out[i].Qty = qty
	}
	return out
}
