package risk

// PayoffMetrics summarizes the profit/loss profile at expiry.
type PayoffMetrics struct {
	MaxProfit           float64   `json:"max_profit"`
	MaxLoss             float64   `json:"max_loss"`
	RiskRewardRatio     float64   `json:"risk_reward_ratio"`
	Breakevens          []float64 `json:"breakevens"`
	IsDefinedRisk       bool      `json:"is_defined_risk"`
	ProbabilityOfProfit float64   `json:"prob_profit_approx"` // Optional
}

// PayoffPoint is a single point on the expiration graph.
type PayoffPoint struct {
	SpotPrice float64 `json:"spot_price"`
	Profit    float64 `json:"profit"`
}

// PayoffCurve represents the P&L at expiry across a range of spot prices.
type PayoffCurve struct {
	Points []PayoffPoint `json:"points"`
}
