package market

import (
	"time"
)

// OptionType represents Call or Put.
type OptionType string

const (
	Call OptionType = "CALL"
	Put  OptionType = "PUT"
)

// ParseOptionType safely parses a string into OptionType
func ParseOptionType(s string) (OptionType, bool) {
	switch s {
	case "CALL", "C", "call", "c":
		return Call, true
	case "PUT", "P", "put", "p":
		return Put, true
	default:
		return "", false
	}
}

// Contract identifies a specific option contract.
type Contract struct {
	Symbol string     `json:"symbol"`
	Expiry time.Time  `json:"expiry"`
	Strike float64    `json:"strike"`
	Type   OptionType `json:"type"`
}

// MarketData holds raw price and volume data.
type MarketData struct {
	Bid          float64 `json:"bid"`
	Ask          float64 `json:"ask"`
	Mid          float64 `json:"mid"`
	Last         float64 `json:"last"`
	Volume       int64   `json:"volume"`
	OpenInterest int64   `json:"open_interest"`
}

// DerivedMetrics holds computed values like spread.
type DerivedMetrics struct {
	Spread     float64 `json:"spread"`
	SpreadPct  float64 `json:"spread_pct"`
	MarkSource string  `json:"mark_source"` // e.g., "MID", "LAST"
}

// QualityFlags holds validation results.
type QualityFlags struct {
	IsTradable           bool     `json:"is_tradable"`             // Strict flag (Strategy ready)
	IsUsableForIV        bool     `json:"is_usable_for_iv"`        // Good price, spread ok
	IsUsableForStrategy  bool     `json:"is_usable_for_strategy"`  // Strict liquidity
	IsUsableForAnalytics bool     `json:"is_usable_for_analytics"` // Present, even if wide spread
	Flags                []string `json:"flags"`
}

// Quote represents a single option quote with all metadata.
type Quote struct {
	Contract Contract       `json:"contract"`
	Market   MarketData     `json:"market"`
	Derived  DerivedMetrics `json:"derived"`
	Quality  QualityFlags   `json:"quality"`
}

// Underlying represents the spot instrument.
type Underlying struct {
	Symbol string  `json:"symbol"`
	Spot   float64 `json:"spot"`
	Source string  `json:"source"`
}

// ExpiryMetadata holds info about an expiration date.
type ExpiryMetadata struct {
	Expiry       time.Time `json:"expiry"`
	TTEYears     float64   `json:"time_to_expiry_years"` // Trading years or calendar years
	DaysToExpiry int       `json:"calendar_days_to_expiry"`
	TradingDays  int       `json:"trading_sessions_to_expiry"`
}

// QualitySummary aggregates counts of good/bad quotes.
type QualitySummary struct {
	TotalQuotes      int            `json:"total_quotes"`
	TradableQuotes   int            `json:"tradable_quotes"`
	RejectedQuotes   int            `json:"rejected_quotes"`
	RejectionReasons map[string]int `json:"rejection_reasons"`
}

// RawDumpMetadata holds info about the original data source.
type RawDumpMetadata struct {
	Stored bool   `json:"stored"`
	S3Key  string `json:"s3_key"`
}

// Snapshot is the canonical representation of the market at a point in time.
type Snapshot struct {
	ID             string           `json:"snapshot_id"`
	AsOf           time.Time        `json:"as_of"`
	Underlying     Underlying       `json:"underlying"`
	Expiries       []ExpiryMetadata `json:"expiries"`
	Quotes         []Quote          `json:"quotes"`
	QualitySummary QualitySummary   `json:"quality_summary"`
	RawDump        RawDumpMetadata  `json:"raw_dump"`
}

// ChainSnapshot represents a structured view of the option chain for a specific expiry.
type ChainSnapshot struct {
	ID                  string                  `json:"chain_snapshot_id"`
	AsOf                time.Time               `json:"as_of"`
	Underlying          Underlying              `json:"underlying"`
	Expiry              ExpiryMetadata          `json:"expiry"`
	ChainState          ChainState              `json:"chain_state"`
	ByStrike            map[string]*StrikeChain `json:"by_strike"` // Map Key is string(strike)
	LiquiditySummary    LiquiditySummary        `json:"liquidity_summary"`
	DownstreamReadySets DownstreamReadySets     `json:"downstream_ready_sets"`
}

type ChainState struct {
	ATMStrike           float64          `json:"atm_strike"`
	ImpliedForward      float64          `json:"implied_forward"`
	StrikeStep          float64          `json:"strike_step"`
	Strikes             []float64        `json:"strikes"`
	EligibleStrikes     []float64        `json:"eligible_strikes"`      // Deprecated: use specific arrays
	EligibleForIV       []float64        `json:"eligible_for_iv"`       // At least one IV-usable option
	EligibleForStrategy []float64        `json:"eligible_for_strategy"` // At least one strategy-usable option
	EligibleForParity   []float64        `json:"eligible_for_parity"`   // Both legs + parity conditions
	EligibilityRules    EligibilityRules `json:"eligibility_rules"`
}

type EligibilityRules struct {
	MaxSpreadPct        float64 `json:"max_spread_pct"`
	MinVolume           int64   `json:"min_volume"`
	MinOpenInterest     int64   `json:"min_open_interest"`
	NearATMStrikeWindow int     `json:"near_atm_strike_window"` // For forward parity calc
	RequireBidAsk       bool    `json:"require_bid_ask"`
}

type StrikeChain struct {
	Moneyness    float64 `json:"moneyness"`     // K/S (spot-based)
	LogMoneyness float64 `json:"log_moneyness"` // ln(K/F) (forward-based)
	Call         *Option `json:"call"`          // Nullable
	Put          *Option `json:"put"`           // Nullable
}

type Option struct {
	Mid          float64      `json:"mid"`
	Bid          float64      `json:"bid"`
	Ask          float64      `json:"ask"`
	Volume       int64        `json:"volume"`
	OpenInterest int64        `json:"open_interest"`
	Quality      QualityFlags `json:"quality"`
}

type LiquiditySummary struct {
	TradableNearATM     bool     `json:"tradable_near_atm"`
	AvgSpreadPctNearATM float64  `json:"avg_spread_pct_near_atm"`
	Notes               []string `json:"notes"`
}

type DownstreamReadySets struct {
	IVSurfaceInputs     []IVPoint           `json:"iv_surface_inputs"`
	StrategyLegUniverse StrategyLegUniverse `json:"strategy_leg_universe"`
}

type IVPoint struct {
	Expiry       string     `json:"expiry"`
	Strike       float64    `json:"strike"`
	Type         OptionType `json:"type"`
	ImpliedVol   float64    `json:"implied_vol"`
	Confidence   float64    `json:"confidence"`
	LogMoneyness float64    `json:"log_moneyness"`
	Weight       float64    `json:"weight"` // Calculated as confidence^p

	MarkPrice    float64  `json:"mark_price"`     // ADDED
	FitErrorAbs  float64  `json:"fit_error_abs"`  // ADDED
	VegaPerPoint float64  `json:"vega_per_point"` // ADDED
	MarkSource   string   `json:"mark_source"`
	Flags        []string `json:"flags"`
}

type StrategyLegUniverse struct {
	AllowedStrikes []float64    `json:"allowed_strikes"`
	AllowedTypes   []OptionType `json:"allowed_types"`
	Notes          []string     `json:"notes"`
}

// SkewFitParams holds quadratic fit coefficients: a + bx + cx^2
type SkewFitParams struct {
	A float64 `json:"a_atm"`
	B float64 `json:"b_slope"`
	C float64 `json:"c_curvature"`
}

type SkewFitQuality struct {
	WeightedRMSE float64  `json:"weighted_rmse"`
	PointsUsed   int      `json:"points_used"`
	Flags        []string `json:"flags"`
}

type SkewFit struct {
	Type    string         `json:"type"` // e.g., "QUADRATIC_WLS"
	Params  SkewFitParams  `json:"params"`
	Quality SkewFitQuality `json:"quality"`
}

type SkewMetrics struct {
	ATMVol     float64 `json:"atm_iv"`
	SkewSlope  float64 `json:"skew_slope"`
	Curvature  float64 `json:"curvature"`
	Confidence float64 `json:"confidence"`
}

type IVSkewSnapshot struct {
	AsOf       time.Time   `json:"as_of"`
	Underlying string      `json:"underlying"`
	Expiry     string      `json:"expiry"`
	Forward    float64     `json:"forward"`
	TTEYears   float64     `json:"time_to_expiry_years"`
	Points     []IVPoint   `json:"points_used"`
	Fit        SkewFit     `json:"fit"`
	Metrics    SkewMetrics `json:"metrics"`
}

type TermStructurePoint struct {
	Expiry     string  `json:"expiry"`
	TTEYears   float64 `json:"tte_years"`
	ATMVol     float64 `json:"atm_iv"`
	Confidence float64 `json:"confidence"`
}

type TermStructureMetrics struct {
	NearExpiry string  `json:"near_expiry"`
	FarExpiry  string  `json:"far_expiry"`
	TermSlope  float64 `json:"term_slope"` // (VolFar - VolNear) / (TFar - TNear)
}

type TermStructure struct {
	Points  []TermStructurePoint `json:"points"`
	Metrics TermStructureMetrics `json:"metrics"`
}

type IVSurfaceSnapshot struct {
	AsOf          time.Time        `json:"as_of"`
	Underlying    string           `json:"underlying"`
	Skews         []IVSkewSnapshot `json:"skews"`
	TermStructure TermStructure    `json:"term_structure"`
}

// IVReference holds the calculated constant maturity IV (e.g., IV30)
type IVReference struct {
	TenorDays  int     `json:"tenor_days"` // e.g., 30
	IV         float64 `json:"iv"`
	Method     string  `json:"method"` // e.g., "VARIANCE_INTERP"
	Confidence float64 `json:"confidence"`
}

// HistoricalContext holds IV Rank and Percentile data
type HistoricalContext struct {
	LookbackDays int     `json:"lookback_days"`
	MinIV        float64 `json:"min_iv"`
	MaxIV        float64 `json:"max_iv"`
	IVRank       float64 `json:"iv_rank"`       // 0.0 to 1.0
	IVPercentile float64 `json:"iv_percentile"` // 0.0 to 1.0
}

// RealizedVol holds historical/realized volatility metrics
type RealizedVol struct {
	WindowDays int     `json:"window_days"` // e.g., 20
	HV         float64 `json:"hv"`
	IVHVRatio  float64 `json:"iv_hv_ratio"`
	IVHVSpread float64 `json:"iv_hv_spread"`
}

// RegimeDecision holds the final classification
type RegimeDecision struct {
	Regime    string   `json:"regime"` // HIGH_VOL, LOW_VOL, TRANSITION
	Bias      string   `json:"bias"`   // BUY_PREMIUM, SELL_PREMIUM, NEUTRAL
	Score     float64  `json:"score"`
	Rationale []string `json:"rationale"`
}

type RegimeTermStructure struct {
	NearExpiry  string  `json:"near_expiry"`
	NextExpiry  string  `json:"next_expiry"`
	NearATMIV   float64 `json:"near_atm_iv"`
	NextATMIV   float64 `json:"next_atm_iv"`
	TermPremium float64 `json:"term_premium"`
}

type RegimeSkew struct {
	ExpiryUsed string  `json:"expiry_used"`
	SkewSlope  float64 `json:"skew_slope"`
	Curvature  float64 `json:"curvature"`
}

// RegimeState is the top-level output of the Regime Detector
type RegimeState struct {
	AsOf              time.Time           `json:"as_of"`
	Underlying        string              `json:"underlying"`
	IVReference       IVReference         `json:"iv_reference"`
	HistoricalContext HistoricalContext   `json:"historical_window"`
	RealizedVol       RealizedVol         `json:"realized_vol"`
	TermStructure     RegimeTermStructure `json:"term_structure"`
	Skew              RegimeSkew          `json:"skew"`
	Decision          RegimeDecision      `json:"decision"`
	Quality           IVQuality           `json:"quality"`
}

const (
	RegimeHighVol    = "HIGH_VOL"
	RegimeLowVol     = "LOW_VOL"
	RegimeTransition = "TRANSITION"
)

const (
	BiasSellPremium = "SELL_PREMIUM"
	BiasBuyPremium  = "BUY_PREMIUM"
	BiasNeutral     = "NEUTRAL_OR_DEFINED_RISK"
)
