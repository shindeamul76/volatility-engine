package strategy

import (
	"time"

	"volatility-engine/internal/domain/market"
)

// StrategyType enum
type StrategyType string

const (
	IronCondor   StrategyType = "IRON_CONDOR"
	IronFly      StrategyType = "IRON_FLY"
	LongStraddle StrategyType = "LONG_STRADDLE"
	LongStrangle StrategyType = "LONG_STRANGLE"
)

// StrategyCandidate represents a fully formed strategy proposal.
type StrategyCandidate struct {
	ID           string           `json:"candidate_id"`
	AsOf         time.Time        `json:"as_of"`
	Underlying   string           `json:"underlying"`
	Expiry       string           `json:"expiry"` // YYYY-MM-DD
	StrategyType StrategyType     `json:"strategy_type"`
	Intent       string           `json:"intent"` // e.g. SHORT_VOL_DEFINED_RISK
	Legs         []StrategyLeg    `json:"legs"`
	Entry        EntryDetails     `json:"entry"`
	Metrics      QuickMetrics     `json:"quick_metrics"`
	Rationale    Rationale        `json:"rationale"`
	Management   ManagementRules  `json:"management"`
	Quality      CandidateQuality `json:"quality"`
}

type StrategyLeg struct {
	Side       string            `json:"side"` // BUY, SELL
	Type       market.OptionType `json:"type"` // CALL, PUT
	Strike     float64           `json:"strike"`
	Qty        int               `json:"qty"`
	Mark       float64           `json:"mark"`
	IV         float64           `json:"iv"`
	Confidence float64           `json:"confidence"`
	Delta      float64           `json:"delta"` // Added for context
}

type EntryDetails struct {
	NetPremium  float64     `json:"net_premium"`
	PremiumType string      `json:"premium_type"` // DEBIT, CREDIT
	Assumptions Assumptions `json:"assumptions"`
}

type Assumptions struct {
	Pricing       string `json:"pricing"`        // MID, BID_ASK
	SlippageModel string `json:"slippage_model"` // NONE_PROTOTYPE
}

type QuickMetrics struct {
	MaxProfit        float64    `json:"max_profit"`
	MaxLossApprox    float64    `json:"max_loss_approx"`
	Breakevens       BreakEvens `json:"breakevens_approx"`
	MinLegConfidence float64    `json:"min_leg_confidence"`
	RiskRewardRatio  float64    `json:"risk_reward_ratio,omitempty"`
}

type BreakEvens struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

type Rationale struct {
	Regime  string   `json:"regime"`
	Reasons []string `json:"reasons"`
	Score   float64  `json:"score"`
}

type ManagementRules struct {
	ProfitTakePctOfCredit    float64 `json:"profit_take_pct_of_credit,omitempty"`
	StopLossMultipleOfCredit float64 `json:"stop_loss_multiple_of_credit,omitempty"`
	CloseIfDTEBelow          int     `json:"close_if_dte_below_days"`
	ProfitTakePctOfDebit     float64 `json:"profit_take_pct_of_debit,omitempty"` // For long strategies
	TimeStopDays             int     `json:"time_stop_days,omitempty"`
}

type CandidateQuality struct {
	Score    float64  `json:"score"`
	Flags    []string `json:"flags"`
	Warnings []string `json:"warnings"`
}

// SelectorInputs bundles all necessary data for the engine.
type SelectorInputs struct {
	Regime       interface{} // Using interface to avoid circular deps if needed, but optimally concrete types
	Surface      market.IVSurfaceSnapshot
	Intel        interface{} // ChainIntelSnapshot
	Chain        market.ChainSnapshot
	Forward      market.ForwardState
	RiskFreeRate float64
}
