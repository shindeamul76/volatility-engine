package main

import (
	"fmt"
	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/quant/risk"
)

func main() {
	// 1. Setup Mock Strategy: Iron Condor on NIFTY
	// Short 24000 Put, Long 23800 Put
	// Short 25000 Call, Long 25200 Call
	// Spot 24500

	spot := 24500.0
	expiry := "2026-02-26" // Dummy

	candidate := market.StrategyCandidate{
		ID:         "TEST_IC_001",
		Underlying: "NIFTY",
		Expiry:     expiry,
		Legs: []market.StrategyLeg{
			{Side: "SELL", Type: market.Put, Strike: 24000, Qty: 50, Mark: 100, IV: 0.15},
			{Side: "BUY", Type: market.Put, Strike: 23800, Qty: 50, Mark: 50, IV: 0.16},
			{Side: "SELL", Type: market.Call, Strike: 25000, Qty: 50, Mark: 80, IV: 0.14},
			{Side: "BUY", Type: market.Call, Strike: 25200, Qty: 50, Mark: 40, IV: 0.15},
		},
		Entry: market.EntryDetails{
			NetPremium:  (100 - 50 + 80 - 40) * 50, // (180 - 90) = 90 * 50 = 4500 credit
			PremiumType: "CREDIT",
		},
	}

	// 2. Test Payoff Analysis
	payoff := risk.AnalyzePayoff(candidate.Legs)

	fmt.Println("--- Payoff Analysis ---")
	fmt.Printf("Max Profit: %.2f\n", payoff.MaxProfit) // Should be Credit = 4500
	fmt.Printf("Max Loss: %.2f\n", payoff.MaxLoss)     // Width 200 - 90 = 110 * 50 = 5500. Loss should be -5500.
	fmt.Printf("Breakevens: %v\n", payoff.Breakevens)
	fmt.Printf("Unbounded? P: %v L: %v\n", payoff.UnboundedProfit, payoff.UnboundedLoss)

	// 3. Test Scenarios
	// Mock Skew Params
	skews := map[string]market.IVSkewSnapshot{
		expiry: {
			Expiry:   expiry,
			Forward:  spot * 1.01, // 1% fwd premium
			TTEYears: 12.0 / 365.0,
			Fit: market.SkewFit{
				Params: market.SkewFitParams{A: 0.15, B: -0.01, C: 0.001},
			},
		},
	}

	grid := risk.DefaultScenarioGrid()
	// Reduce grid for test speed
	grid.TimeSteps = []int{0, 10}

	scenarios := risk.RunScenarios(candidate, spot, 0.05, skews, grid) // r=5%

	fmt.Println("\n--- Scenario Analysis ---")
	fmt.Printf("Generated %d scenarios\n", len(scenarios.Results))
	fmt.Printf("Worst Case PnL: %.2f (Scenario: Spot %.2f, Vol %.2f)\n",
		scenarios.WorstCase.PnL,
		scenarios.WorstCase.Scenario.SpotChangePct,
		scenarios.WorstCase.Scenario.VolChange)

	// Dump JSON for inspection
	/*
		b, _ := json.MarshalIndent(scenarios, "", "  ")
		fmt.Println(string(b))
	*/

	// Check Greeks
	netGreeks := risk.CalculateNetGreeks(candidate.Legs)
	fmt.Printf("Net Greeks Delta (Placeholder): %.2f\n", netGreeks.Delta) // Likely 0 as implemented currently
}
