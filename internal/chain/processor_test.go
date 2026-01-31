package chain_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"volatility-engine/internal/chain"
	"volatility-engine/internal/domain/market"
)

func TestGenerateChain(t *testing.T) {
	// Setup Mock Snapshot
	now := time.Now()
	expiry := now.AddDate(0, 0, 5)

	snap := &market.Snapshot{
		ID:   "snap_test",
		AsOf: now,
		Underlying: market.Underlying{
			Symbol: "TEST",
			Spot:   20000.0,
		},
		Expiries: []market.ExpiryMetadata{
			{Expiry: expiry, DaysToExpiry: 5},
		},
		Quotes: []market.Quote{
			// ATM Call - Eligible
			{
				Contract: market.Contract{Symbol: "TEST", Strike: 20000, Type: market.Call, Expiry: expiry},
				Market:   market.MarketData{Bid: 120, Ask: 124, Mid: 122},
				Quality:  market.QualityFlags{IsTradable: true},
			},
			// ATM Put - Eligible
			{
				Contract: market.Contract{Symbol: "TEST", Strike: 20000, Type: market.Put, Expiry: expiry},
				Market:   market.MarketData{Bid: 110, Ask: 114, Mid: 112},
				Quality:  market.QualityFlags{IsTradable: true},
			},
			// OTM Put - WIDE SPREAD
			{
				Contract: market.Contract{Symbol: "TEST", Strike: 19000, Type: market.Put, Expiry: expiry},
				Market:   market.MarketData{Bid: 200, Ask: 500, Mid: 350}, // Spread 300/350 = ~85%
				Quality:  market.QualityFlags{IsTradable: true},           // Upstream said ok, but chain rules should catch it
			},
		},
	}

	// 2. Generate Chain
	proc := chain.NewProcessor(0.05)
	cs, err := proc.GenerateChain(snap, expiry)
	if err != nil {
		t.Fatalf("GenerateChain failed: %v", err)
	}

	// 1. Verify Structure
	if cs.ChainState.ATMStrike != 20000 {
		t.Errorf("Expected ATM 20000, got %f", cs.ChainState.ATMStrike)
	}

	// 2. Verify Eligibility
	eligibleCount := len(cs.ChainState.EligibleStrikes)
	if eligibleCount != 1 {
		// Only 20000 should be eligible. 19000 has wide spread.
		t.Errorf("Expected 1 eligible strike (20000), got %d: %v", eligibleCount, cs.ChainState.EligibleStrikes)
	}

	// 3. Verify Flags for 19000 Put
	otmChain := cs.ByStrike["19000"]
	if otmChain == nil {
		t.Fatal("Expected 19000 strike to exist in map")
	}
	if otmChain.Put.Quality.IsTradable {
		t.Error("Expected 19000 Put to be non-tradable due to spread")
	}

	// Debug JSON
	bytes, _ := json.MarshalIndent(cs, "", "  ")
	fmt.Printf("Chain JSON:\n%s\n", string(bytes))
}
