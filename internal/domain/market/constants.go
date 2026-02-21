package market

// Analysis Constants
const (
	DaysPerYear       = 365.0
	MinTTEYears       = 1e-6 // ~30 seconds. Below this, treat as expired/intrinsic.
	DefaultRiskFree   = 0.05
	DefaultVolatility = 0.20
)

// Quality Flags & Reasons
const (
	FlagBadPrice         = "BAD_PRICE"
	FlagCrossedMarket    = "CROSSED_MARKET"
	FlagBadMidPrice      = "BAD_MID_PRICE"
	FlagWideSpread       = "WIDE_SPREAD"
	FlagWideSpreadAbs    = "WIDE_SPREAD_ABS"
	FlagLowPremium       = "LOW_PREMIUM"
	FlagIlliquid         = "ILLIQUID"
	FlagIlliquidStrategy = "ILLIQUID_STRATEGY"
	FlagLKPFallback      = "LTP_FALLBACK"
)

// Defaults for Heuristics
const (
	DefaultMaxSpreadPct        = 0.10
	DefaultMaxSpreadAbs        = 5.0
	DefaultNearATMStrikeWindow = 5
)
