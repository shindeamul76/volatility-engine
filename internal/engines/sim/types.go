package sim

import (
	"volatility-engine/internal/quant/pricing"
)

// Pricer wraps the core pricing logic.
type Pricer interface {
	Price(ctx pricing.PricingContext) pricing.Result
}

// VolModel provides volatility for a specific scenario.
// It abstracts away the skew interpolation or sticky-strike logic.
type VolModel interface {
	// GetSigma returns the annualized volatility for a given strike and time-to-expiry.
	// This might involve re-interpolating the skew at the new spot price (sticky delta)
	// or simple offset (sticky strike).
	GetSigma(strike float64, tte float64, currentSpot float64) float64
}

// SimContext holds shared data for a simulation run (e.g. Risk Free Rate).
type SimContext struct {
	RiskFreeRate float64
	Now          float64 // TTE at t=0
}

// DefaultPricer uses the quant/pricing package directly.
type DefaultPricer struct{}

func (p DefaultPricer) Price(ctx pricing.PricingContext) pricing.Result {
	return pricing.Calculate(ctx)
}
