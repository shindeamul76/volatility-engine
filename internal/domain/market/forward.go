package market

import "time"

// ForwardState captures the implied forward calculation details
type ForwardState struct {
	ID         string     `json:"forward_state_id"`
	AsOf       time.Time  `json:"as_of"`
	Underlying Underlying `json:"underlying"`
	Expiry     struct {
		Date     string  `json:"date"`
		TTEYears float64 `json:"time_to_expiry_years"`
	} `json:"expiry"`
	Rates            Rates             `json:"rates"`
	SourcePreference []string          `json:"source_preference"`
	SelectedSource   string            `json:"selected_source"`
	Forward          ForwardValue      `json:"forward"`
	ForwardByStrike  []ForwardByStrike `json:"forward_by_strike"`
	FiltersUsed      ForwardFilters    `json:"filters_used"`
}

// Rates holds risk-free rate and discount factor
type Rates struct {
	RiskFreeRateCC float64 `json:"risk_free_rate_cc"`
	DiscountFactor float64 `json:"discount_factor"`
}

// ForwardValue is the aggregated forward
type ForwardValue struct {
	Mid        float64  `json:"mid"`
	Confidence float64  `json:"confidence"`
	Method     string   `json:"method"`
	Notes      []string `json:"notes"`
}

// ForwardByStrike captures per-strike parity calculation
type ForwardByStrike struct {
	Strike        float64     `json:"strike"`
	Call          *OptionMark `json:"call"`
	Put           *OptionMark `json:"put"`
	ParityForward *float64    `json:"parity_forward"`
	Weight        float64     `json:"weight"`
	QualityFlags  []string    `json:"quality_flags"`
}

// OptionMark is minimal option pricing info for forward calc
type OptionMark struct {
	Mark       float64 `json:"mark"`
	SpreadPct  float64 `json:"spread_pct"`
	IsTradable bool    `json:"is_tradable"`
}

// ForwardFilters used for forward calculation
type ForwardFilters struct {
	MaxSpreadPctPerLeg  float64 `json:"max_spread_pct_per_leg"`
	NearATMStrikeWindow int     `json:"near_atm_strike_window"`
	RequireBothCallPut  bool    `json:"require_both_call_put"`
}

// PricingRequest is the input for pricing
type PricingRequest struct {
	RequestID   string             `json:"request_id"`
	AsOf        time.Time          `json:"as_of"`
	Instrument  PricingInstrument  `json:"instrument"`
	MarketCtx   MarketContext      `json:"market_context"`
	Rates       Rates              `json:"rates"`
	Model       PricingModel       `json:"model"`
	Conventions PricingConventions `json:"conventions"`
}

// PricingInstrument identifies the option
type PricingInstrument struct {
	Underlying string  `json:"underlying"`
	Expiry     string  `json:"expiry"`
	Strike     float64 `json:"strike"`
	Type       string  `json:"type"`
}

// MarketContext has spot/forward info
type MarketContext struct {
	Spot          float64 `json:"spot"`
	Forward       float64 `json:"forward"`
	ForwardSource string  `json:"forward_source"`
	TTEYears      float64 `json:"time_to_expiry_years"`
}

// PricingModel identifies the model used
type PricingModel struct {
	Name            string  `json:"name"`
	Volatility      float64 `json:"volatility"`
	VolatilityUnits string  `json:"volatility_units"`
}

// PricingConventions for output units
type PricingConventions struct {
	ThetaUnit string `json:"theta_unit"`
	VegaUnit  string `json:"vega_unit"`
	DayCount  string `json:"day_count"`
}

// PricingResult is the output from pricing
type PricingResult struct {
	RequestID  string         `json:"request_id"`
	ModelUsed  string         `json:"model_used"`
	InputsEcho PricingInputs  `json:"inputs_echo"`
	Outputs    PricingOutputs `json:"outputs"`
	Quality    PricingQuality `json:"quality"`
}

// PricingInputs echoes the inputs
type PricingInputs struct {
	Forward        float64 `json:"forward"`
	Strike         float64 `json:"strike"`
	TTEYears       float64 `json:"time_to_expiry_years"`
	RiskFreeRateCC float64 `json:"risk_free_rate_cc"`
	DiscountFactor float64 `json:"discount_factor"`
	Volatility     float64 `json:"volatility"`
	Type           string  `json:"type"`
}

// PricingOutputs has price and greeks
type PricingOutputs struct {
	TheoreticalPrice float64              `json:"theoretical_price"`
	Greeks           GreeksOutput         `json:"greeks"`
	Intermediates    PricingIntermediates `json:"intermediates"`
}

// GreeksOutput is styled for JSON
type GreeksOutput struct {
	Delta float64 `json:"delta"`
	Gamma float64 `json:"gamma"`
	Theta float64 `json:"theta"`
	Vega  float64 `json:"vega"`
}

// PricingIntermediates has d1, d2, etc.
type PricingIntermediates struct {
	D1  float64 `json:"d1"`
	D2  float64 `json:"d2"`
	Nd1 float64 `json:"nd1"`
	Nd2 float64 `json:"nd2"`
}

// PricingQuality flags issues
type PricingQuality struct {
	OK       bool     `json:"ok"`
	Flags    []string `json:"flags"`
	Warnings []string `json:"warnings"`
}
