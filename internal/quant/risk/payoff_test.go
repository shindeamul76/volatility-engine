package risk

import (
	"math"
	"testing"
	"volatility-engine/internal/domain/market"
)

func TestPayoff_LongCall(t *testing.T) {
	// Long 24000 Call @ 200
	legs := []market.StrategyLeg{
		{Side: "BUY", Type: market.Call, Strike: 24000, Qty: 1, Mark: 200},
	}

	p := AnalyzePayoff(legs)

	if !p.UnboundedProfit {
		t.Errorf("Expected unbounded profit for long call")
	}
	if p.MaxLoss != -200 {
		t.Errorf("Expected max loss -200, got %f", p.MaxLoss)
	}
	if len(p.Breakevens) != 1 || math.Abs(p.Breakevens[0]-24200) > 0.01 {
		t.Errorf("Expected breakeven at 24200, got %v", p.Breakevens)
	}
}

func TestPayoff_IronCondor(t *testing.T) {
	// Short 24000 Put @ 100, Long 23800 Put @ 50  (Credit 50, Width 200)
	// Short 25000 Call @ 80, Long 25200 Call @ 40   (Credit 40, Width 200)
	// Total Credit = 90
	// Max Loss = Width - Credit = 200 - 90 = 110

	legs := []market.StrategyLeg{
		{Side: "SELL", Type: market.Put, Strike: 24000, Qty: 1, Mark: 100},
		{Side: "BUY", Type: market.Put, Strike: 23800, Qty: 1, Mark: 50},
		{Side: "SELL", Type: market.Call, Strike: 25000, Qty: 1, Mark: 80},
		{Side: "BUY", Type: market.Call, Strike: 25200, Qty: 1, Mark: 40},
	}

	p := AnalyzePayoff(legs)

	expectedMaxProfit := 90.0
	expectedMaxLoss := -110.0

	if math.Abs(p.MaxProfit-expectedMaxProfit) > 0.001 {
		t.Errorf("Expected max profit %.2f, got %.2f", expectedMaxProfit, p.MaxProfit)
	}
	if math.Abs(p.MaxLoss-expectedMaxLoss) > 0.001 {
		t.Errorf("Expected max loss %.2f, got %.2f", expectedMaxLoss, p.MaxLoss)
	}

	// Breakevens:
	// Lower: Put Strike 24000 - Credit 90 = 23910
	// Upper: Call Strike 25000 + Credit 90 = 25090
	if len(p.Breakevens) != 2 {
		t.Fatalf("Expected 2 breakevens, got %d", len(p.Breakevens))
	}
	if math.Abs(p.Breakevens[0]-23910) > 0.1 {
		t.Errorf("Expected Lower BE 23910, got %f", p.Breakevens[0])
	}
	if math.Abs(p.Breakevens[1]-25090) > 0.1 {
		t.Errorf("Expected Upper BE 25090, got %f", p.Breakevens[1])
	}
}
