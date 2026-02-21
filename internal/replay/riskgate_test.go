package replay

import (
	"testing"

	"volatility-engine/internal/config"
	"volatility-engine/internal/domain/market"
)

func defaultRiskGateCfg() config.RiskGateConfig {
	return config.RiskGateConfig{
		Enabled:             true,
		MaxDebitPerTradeINR: 25000,
		MaxLossPerTradeINR:  50000,
		MaxTotalRiskINR:     80000,
		MaxDailyLossINR:     10000,
		MaxDrawdownPct:      0.10,
		RiskPerTradePct:     0.02,
		MaxLotsPerTrade:     10,
	}
}

func makeCandidate(premiumType string, netPremium, maxLoss float64) market.StrategyCandidate {
	return market.StrategyCandidate{
		ID:           "test_strat_1",
		StrategyType: market.LongStraddle,
		Entry: market.EntryDetails{
			NetPremium:  netPremium,
			PremiumType: premiumType,
		},
		Metrics: market.QuickMetrics{
			MaxLossApprox: maxLoss,
		},
		Legs: []market.StrategyLeg{
			{Side: "BUY", Type: market.Call, Strike: 23000, Qty: 1},
			{Side: "BUY", Type: market.Put, Strike: 23000, Qty: 1},
		},
	}
}

func TestRiskGate_Approved(t *testing.T) {
	rg := NewRiskGate(defaultRiskGateCfg())
	pf := NewPortfolio(100000, 65)
	pf.StartOfDay(100000)

	// Debit = 200 points * 65 = 13000 INR per lot
	// Budget = 100000 * 0.02 = 2000 → qty = floor(2000/13000) = 0
	// But let's make premium small enough to afford: 20 * 65 = 1300
	candidate := makeCandidate("DEBIT", 20, 20)

	verdict := rg.Evaluate(candidate, pf, 100000, 100000, 65)

	if verdict.Action != "APPROVED" && verdict.Action != "RESIZED" {
		t.Errorf("Expected APPROVED or RESIZED, got %s: %v", verdict.Action, verdict.Reasons)
	}
	if verdict.Qty < 1 {
		t.Errorf("Expected qty >= 1, got %d", verdict.Qty)
	}
}

func TestRiskGate_RejectedMaxDebit(t *testing.T) {
	cfg := defaultRiskGateCfg()
	cfg.MaxDebitPerTradeINR = 5000 // Very low
	rg := NewRiskGate(cfg)
	pf := NewPortfolio(100000, 65)
	pf.StartOfDay(100000)

	// Debit = 200 * 65 = 13000 > 5000
	candidate := makeCandidate("DEBIT", 200, 200)

	verdict := rg.Evaluate(candidate, pf, 100000, 100000, 65)

	if verdict.Action != "REJECTED" {
		t.Errorf("Expected REJECTED, got %s: %v", verdict.Action, verdict.Reasons)
	}
}

func TestRiskGate_RejectedMaxLoss(t *testing.T) {
	cfg := defaultRiskGateCfg()
	cfg.MaxLossPerTradeINR = 500 // Very low
	rg := NewRiskGate(cfg)
	pf := NewPortfolio(100000, 65)
	pf.StartOfDay(100000)

	// MaxLoss = 20 * 65 = 1300 > 500
	candidate := makeCandidate("DEBIT", 20, 20)

	verdict := rg.Evaluate(candidate, pf, 100000, 100000, 65)

	if verdict.Action != "REJECTED" {
		t.Errorf("Expected REJECTED, got %s: %v", verdict.Action, verdict.Reasons)
	}
}

func TestRiskGate_RejectedDailyLossStop(t *testing.T) {
	rg := NewRiskGate(defaultRiskGateCfg())
	pf := NewPortfolio(100000, 65)
	pf.StartOfDay(100000) // Day started at 100k

	// Current equity = 89000 → daily PnL = -11000 < -10000
	candidate := makeCandidate("DEBIT", 20, 20)

	verdict := rg.Evaluate(candidate, pf, 89000, 100000, 65)

	if verdict.Action != "REJECTED" {
		t.Errorf("Expected REJECTED for daily loss, got %s: %v", verdict.Action, verdict.Reasons)
	}
}

func TestRiskGate_RejectedDrawdownStop(t *testing.T) {
	rg := NewRiskGate(defaultRiskGateCfg())
	pf := NewPortfolio(100000, 65)
	pf.StartOfDay(90000) // Day started low (no daily stop trigger)

	// Peak = 100000, current = 89000 → drawdown = -11% > -10%
	candidate := makeCandidate("DEBIT", 20, 20)

	verdict := rg.Evaluate(candidate, pf, 89000, 100000, 65)

	if verdict.Action != "REJECTED" {
		t.Errorf("Expected REJECTED for drawdown, got %s: %v", verdict.Action, verdict.Reasons)
	}
}

func TestRiskGate_RejectedInsufficientCapital(t *testing.T) {
	cfg := defaultRiskGateCfg()
	cfg.RiskPerTradePct = 0.001 // Very small budget
	rg := NewRiskGate(cfg)
	pf := NewPortfolio(100000, 65)
	pf.StartOfDay(100000)

	// Budget = 100000 * 0.001 = 100 INR
	// Risk per lot = 200*65 = 13000 → qty = floor(100/13000) = 0
	candidate := makeCandidate("DEBIT", 200, 200)

	verdict := rg.Evaluate(candidate, pf, 100000, 100000, 65)

	if verdict.Action != "REJECTED" {
		t.Errorf("Expected REJECTED for insufficient capital, got %s: %v", verdict.Action, verdict.Reasons)
	}
}

func TestRiskGate_ClampToMaxLots(t *testing.T) {
	cfg := defaultRiskGateCfg()
	cfg.MaxLotsPerTrade = 2
	cfg.RiskPerTradePct = 0.50 // 50% → huge budget
	rg := NewRiskGate(cfg)
	pf := NewPortfolio(1000000, 65)
	pf.StartOfDay(1000000)

	// Budget = 500000, risk/lot = 10*65=650 → raw qty=769, clamped to 2
	candidate := makeCandidate("DEBIT", 10, 10)

	verdict := rg.Evaluate(candidate, pf, 1000000, 1000000, 65)

	if verdict.Qty > 2 {
		t.Errorf("Expected qty clamped to 2, got %d", verdict.Qty)
	}
}

func TestRiskGate_Disabled(t *testing.T) {
	cfg := defaultRiskGateCfg()
	cfg.Enabled = false
	rg := NewRiskGate(cfg)
	pf := NewPortfolio(100000, 65)

	candidate := makeCandidate("DEBIT", 200, 200)

	verdict := rg.Evaluate(candidate, pf, 100000, 100000, 65)

	if verdict.Action != "APPROVED" {
		t.Errorf("Expected APPROVED when disabled, got %s", verdict.Action)
	}
}

func TestRiskGate_TotalRiskCap(t *testing.T) {
	cfg := defaultRiskGateCfg()
	cfg.MaxTotalRiskINR = 1500 // Very small
	cfg.RiskPerTradePct = 0.10
	rg := NewRiskGate(cfg)
	pf := NewPortfolio(100000, 65)
	pf.StartOfDay(100000)

	// Simulate an open position with risk
	pf.Positions["pos_existing"] = &Position{
		ID:              "pos_existing",
		MaxLossEstimate: 1400, // Nearly at cap
	}

	// Remaining = 1500 - 1400 = 100. Risk per lot = 10*65 = 650 > 100
	candidate := makeCandidate("DEBIT", 10, 10)

	verdict := rg.Evaluate(candidate, pf, 100000, 100000, 65)

	if verdict.Action != "REJECTED" {
		t.Errorf("Expected REJECTED for total risk cap, got %s: %v", verdict.Action, verdict.Reasons)
	}
}

func TestApplyQtyToLegs(t *testing.T) {
	legs := []ReplayLeg{
		{Side: "BUY", Strike: 23000, Qty: 1},
		{Side: "SELL", Strike: 23500, Qty: 1},
	}

	updated := ApplyQtyToLegs(legs, 3)

	for _, l := range updated {
		if l.Qty != 3 {
			t.Errorf("Expected qty 3, got %d", l.Qty)
		}
	}

	// Original should be unchanged
	if legs[0].Qty != 1 {
		t.Errorf("Original qty mutated, expected 1, got %d", legs[0].Qty)
	}
}
