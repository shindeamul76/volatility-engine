package risk

import (
	"time"

	"volatility-engine/internal/domain/market"
)

// StrategyRiskReport is the user-facing report structure.
type StrategyRiskReport struct {
	Meta struct {
		AsOf       time.Time `json:"as_of"`
		Underlying string    `json:"underlying"`
		Spot       float64   `json:"spot"`
		Expiry     string    `json:"expiry"`
		Regime     string    `json:"regime"`
		IV30       float64   `json:"iv30"`
		IVRank     float64   `json:"iv_rank"`
	} `json:"meta"`

	Candidate market.StrategyCandidate `json:"candidate"`

	Payoff struct {
		Breakevens      []float64 `json:"breakevens"`
		MaxProfit       float64   `json:"max_profit"`
		MaxLoss         float64   `json:"max_loss"`
		UnboundedLoss   bool      `json:"unbounded_loss"`
		UnboundedProfit bool      `json:"unbounded_profit"`
		RiskReward      float64   `json:"risk_reward_ratio"`
	} `json:"payoff"`

	Greeks struct {
		Delta float64 `json:"delta"`
		Gamma float64 `json:"gamma"`
		Theta float64 `json:"theta"`
		Vega  float64 `json:"vega"`
	} `json:"greeks"`

	Scenarios struct {
		GridSpec  ScenarioGridSpec `json:"grid_spec"`
		WorstCase ScenarioResult   `json:"worst_case"`
		Surface   []ScenarioResult `json:"results,omitempty"` // Full list or empty if too large
	} `json:"scenarios"`
}
