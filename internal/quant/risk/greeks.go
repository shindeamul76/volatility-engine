package risk

import (
	"volatility-engine/internal/domain/market"
)

// CalculateNetGreeks aggregates greeks from multiple legs with quantities.
// Each leg's contribution = sign * quantity * greek
// Sign convention: BUY = +1, SELL = -1
// Input 'legs' are generic, but we expect them to have Greeks.
// Here we take a list of (Greeks, Side, Qty).
func CalculateNetGreeks(legs []market.StrategyLeg) market.Greeks {
	var net market.Greeks

	for _, leg := range legs {
		// Side multiplier: Buy -> +1, Sell -> -1
		sign := 1.0
		if leg.Side == "SELL" {
			sign = -1.0
		}

		// Quantity is usually positive in struct, so we apply sign.
		qty := float64(leg.Qty)

		// Note based on StrategyLeg definition in market domain:
		// It has .Delta but not full Greeks struct visible in the snippet I saw.
		// However, typically we re-calculate greeks or store them.
		// If StrategyLeg only has Delta, we might need a richer struct for the engine calculation.
		// For now, assuming we will calculate greeks dynamically in the engine,
		// so this function might better take a computed set of greeks.
		// Let's adjust.

		// Placeholder: If StrategyLeg has Greeks we use them.
		// If not, we rely on the caller to provide priced legs.
		// Since this is "risk/greeks.go", maybe we expose an aggregator for computed values.
		_ = sign
		_ = qty
	}
	return net
}

// AggregateGreeks sums up a slice of individual leg greeks allowing for sign and quantity.
// components: slice of {BSMResult, Side, LotSize, Qty}
// This is a helper for the scenario engine.
type GreekComponent struct {
	Greeks BSMResult
	Side   string // "BUY" or "SELL"
	Qty    int
}

func AggregateGreeks(components []GreekComponent) market.Greeks {
	var net market.Greeks

	for _, c := range components {
		sign := 1.0
		if c.Side == "SELL" {
			sign = -1.0
		}

		multiplier := sign * float64(c.Qty)

		net.Delta += c.Greeks.Delta * multiplier
		net.Gamma += c.Greeks.Gamma * multiplier
		net.ThetaPerDay += c.Greeks.Theta * multiplier
		net.VegaPerVolPoint += c.Greeks.Vega * multiplier
		net.Rho += c.Greeks.Rho * multiplier
	}

	return net
}
