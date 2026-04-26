package market

import (
	"time"
)

// StrategyType enum
type StrategyType string

const (
	IronCondor     StrategyType = "IRON_CONDOR"
	IronFly        StrategyType = "IRON_FLY"
	LongStraddle   StrategyType = "LONG_STRADDLE"
	LongStrangle   StrategyType = "LONG_STRANGLE"
	BullPutSpread  StrategyType = "BULL_PUT_SPREAD"
	BearCallSpread StrategyType = "BEAR_CALL_SPREAD"
)

// VariantParams records how a candidate was parameterised for audit/debug.
type VariantParams struct {
	VariantID       string  `json:"variant_id"` // e.g. "IC-2"
	ShortDeltaCall  float64 `json:"short_delta_call,omitempty"`
	ShortDeltaPut   float64 `json:"short_delta_put,omitempty"`
	WingWidthPoints float64 `json:"wing_width_points,omitempty"`
	WingWidthPct    float64 `json:"wing_width_pct,omitempty"`
	AtmOffset       float64 `json:"atm_offset_strikes,omitempty"` // For strangle distance
	Note            string  `json:"note,omitempty"`
}

// ExpiryScore holds the scoring result for an expiry during expiry selection.
type ExpiryScore struct {
	Expiry         string  `json:"expiry"`
	Score          float64 `json:"score"`
	SkewQuality    float64 `json:"skew_quality"`
	LiquidityScore float64 `json:"liquidity_score"`
	DTEFitness     float64 `json:"dte_fitness"`
	RegimeCompat   float64 `json:"regime_compat"`
	Notes          string  `json:"notes"`
}

// StrategyCandidate represents a fully formed strategy proposal.
type StrategyCandidate struct {
	ID               string           `json:"candidate_id"`
	AsOf             time.Time        `json:"as_of"`
	Underlying       string           `json:"underlying"`
	Expiry           string           `json:"expiry"` // YYYY-MM-DD
	StrategyType     StrategyType     `json:"strategy_type"`
	Intent           string           `json:"intent"` // e.g. SHORT_VOL_DEFINED_RISK
	Legs             []StrategyLeg    `json:"legs"`
	Entry            EntryDetails     `json:"entry"`
	EntryMid         float64          `json:"entry_mid"`         // Net cost/credit at mid prices
	EntryLiquidation float64          `json:"entry_liquidation"` // Realistic: BUY@ask, SELL@bid
	Metrics          QuickMetrics     `json:"quick_metrics"`
	Rationale        Rationale        `json:"rationale"`
	Management       ManagementRules  `json:"management"`
	Quality          CandidateQuality `json:"quality"`
	Variant          VariantParams    `json:"variant_params"`
}

type StrategyLeg struct {
	Side       string     `json:"side"` // BUY, SELL
	Type       OptionType `json:"type"` // CALL, PUT
	Strike     float64    `json:"strike"`
	Qty        int        `json:"qty"`
	Mark       float64    `json:"mark"` // Mid price
	Bid        float64    `json:"bid"`
	Ask        float64    `json:"ask"`
	IV         float64    `json:"iv"`
	Confidence float64    `json:"confidence"`
	Delta      float64    `json:"delta"`
	OI         int64      `json:"oi"`
	Volume     int64      `json:"volume"`
	SpreadPct  float64    `json:"spread_pct"` // (ask-bid)/mid
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
	MarginReq        float64    `json:"margin_req,omitempty"` // Approximate SPAN + Exposure margin per lot
	Breakevens       BreakEvens `json:"breakevens_approx"`
	MinLegConfidence float64    `json:"min_leg_confidence"`
	RiskRewardRatio  float64    `json:"risk_reward_ratio,omitempty"`
}

type BreakEvens struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

// Rationale explains why a strategy was selected.
type Rationale struct {
	Regime         string   `json:"regime"`
	SelectionBasis string   `json:"selection_basis"` // The technical trigger (e.g. "High Vol + Optimal DTE")
	Thesis         string   `json:"thesis"`          // The "Mindset" or hypothesis (e.g. "Expect Mean Reversion")
	LegSelection   string   `json:"leg_selection"`   // Why specific strikes? (e.g. "Sold 20 Delta for high prob")
	Reasons        []string `json:"reasons"`         // Quick bullet summary
	Score          float64  `json:"score"`
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
// Note: Since everything is in market/domain, we can use concrete types now.
type SelectorInputs struct {
	Regime       RegimeState
	Surface      IVSurfaceSnapshot
	Intel        ChainIntelSnapshot
	Chain        ChainSnapshot
	Forward      ForwardState
	RiskFreeRate float64
}
