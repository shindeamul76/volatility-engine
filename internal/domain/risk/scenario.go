package risk

import (
	"volatility-engine/internal/domain/market"
)

// ScenarioGridSpec defines the dimensions of the simulation grid.
type ScenarioGridSpec struct {
	SpotShocks     []float64 `json:"spot_shocks_pct"`   // e.g. -0.10, -0.05, 0, 0.05, 0.10
	VolShocks      []float64 `json:"vol_shocks_points"` // e.g. -5, 0, +5 (vol points)
	TimeSteps      []int     `json:"time_steps_days"`   // e.g. 0 (today), 7, 14...
	ScenariosCount int       `json:"total_scenarios_count"`
}

// ScenarioPoint represents a single coordinate in the simulation grid.
type ScenarioPoint struct {
	SpotChangePct float64 `json:"spot_change_pct"`
	VolChange     float64 `json:"vol_change_points"` // Absolute change in Vol (e.g. +0.02 for +2%)
	DaysForward   int     `json:"days_forward"`
}

// ScenarioResult holds the valuation outcome for a specific scenario.
type ScenarioResult struct {
	Scenario ScenarioPoint `json:"scenario"`

	EstimatedSpot float64 `json:"estimated_spot"`
	EstimatedDate string  `json:"estimated_date"` // YYYY-MM-DD

	StrategyValue float64 `json:"strategy_value"` // Current MTM of the strategy
	PnL           float64 `json:"pnl"`            // StrategyValue - EntryCost
	PnLPct        float64 `json:"pnl_pct"`        // PnL / EntryCost (or Margin)

	Greeks market.Greeks `json:"greeks"` // Net greeks for the strategy
}

// ScenarioSurface represents the full grid of results.
type ScenarioSurface struct {
	Spec    ScenarioGridSpec `json:"grid_spec"`
	Results []ScenarioResult `json:"results"`
}
