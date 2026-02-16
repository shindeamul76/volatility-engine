package risk

import (
	"time"
	"volatility-engine/internal/domain/market"
)

// RiskReport is the top-level container for risk analysis.
type RiskReport struct {
	Meta      ReportMeta               `json:"meta"`
	Candidate market.StrategyCandidate `json:"candidate"`

	Payoff    PayoffMetrics    `json:"payoff"`
	Greeks    market.Greeks    `json:"greeks"`
	Scenarios ScenarioAnalysis `json:"scenarios"`
}

type ReportMeta struct {
	AsOf       time.Time `json:"as_of"`
	Underlying string    `json:"underlying"`
	Spot       float64   `json:"spot"`
	Expiry     string    `json:"expiry"`
	Regime     string    `json:"regime"`
	IV30       float64   `json:"iv30"`
	IVRank     float64   `json:"iv_rank"`
}

// PayoffMetrics summarizes the profit/loss profile at expiry.
type PayoffMetrics struct {
	EntryCost       float64       `json:"entry_cost"` // Net premium paid (debit) or received (credit, negative?) - checking sign convention
	MaxProfit       float64       `json:"max_profit"`
	MaxLoss         float64       `json:"max_loss"`
	UnboundedProfit bool          `json:"unbounded_profit"`
	UnboundedLoss   bool          `json:"unbounded_loss"`
	Breakevens      []float64     `json:"breakevens"`
	RiskRewardRatio float64       `json:"risk_reward_ratio"`
	ProfilePoints   []PayoffPoint `json:"profile_points"` // For charting
}

type PayoffPoint struct {
	Spot   float64 `json:"spot"`
	Profit float64 `json:"profit"`
}

// ScenarioAnalysis contains the results of the scenario grid.
type ScenarioAnalysis struct {
	GridSpec   ScenarioGridSpec `json:"grid_spec"`
	WorstCase  ScenarioResult   `json:"worst_case"`
	BestCase   ScenarioResult   `json:"best_case"`
	BaseCase   ScenarioResult   `json:"base_case"`         // Scenario with 0 shocks
	MaxMtmLoss float64          `json:"max_loss_grid_mtm"` // Worst PnL over entire grid (can exceed expiry max loss)
	Results    []ScenarioResult `json:"results,omitempty"`
}

// ScenarioGridSpec defines the dimensions of the simulation grid.
type ScenarioGridSpec struct {
	SpotShocks     []float64 `json:"spot_shocks_pct"`
	VolShocksAbs   []float64 `json:"vol_shocks_abs"` // Absolute σ change (e.g., -0.10, -0.05, 0, +0.05, +0.10)
	TimeSteps      []int     `json:"time_steps_days"`
	ScenariosCount int       `json:"total_scenarios_count"`
}

// ScenarioPoint represents a single coordinate in the simulation grid.
type ScenarioPoint struct {
	SpotChangePct float64 `json:"spot_change_pct"`
	VolChange     float64 `json:"vol_change_points"`
	DaysForward   int     `json:"days_forward"`
}

// ScenarioResult holds the valuation outcome for a specific scenario.
type ScenarioResult struct {
	Scenario ScenarioPoint `json:"scenario"`

	EstimatedSpot float64 `json:"estimated_spot"`
	EstimatedDate string  `json:"estimated_date"` // YYYY-MM-DD
	EstimatedFwd  float64 `json:"estimated_fwd"`

	StrategyValue float64 `json:"strategy_value"` // Current MTM of the strategy
	PnL           float64 `json:"pnl"`            // StrategyValue - EntryCost

	Greeks market.Greeks `json:"greeks"`
}
