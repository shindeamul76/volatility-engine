package market

import "time"

// IVRequest is the input for IV solving
type IVRequest struct {
	ID         string         `json:"iv_request_id"`
	AsOf       time.Time      `json:"as_of"`
	Instrument IVInstrument   `json:"instrument"`
	Market     IVMarket       `json:"market"`
	Context    IVContext      `json:"context"`
	Rates      Rates          `json:"rates"`
	Settings   SolverSettings `json:"solver_settings"`
}

// IVInstrument identifies the option
type IVInstrument struct {
	Underlying string     `json:"underlying"`
	Expiry     string     `json:"expiry"`
	Strike     float64    `json:"strike"`
	Type       OptionType `json:"type"`
}

// IVMarket has market price info
type IVMarket struct {
	MarkPrice    float64 `json:"mark_price"`
	MarkSource   string  `json:"mark_source"` // MID, LTP
	Bid          float64 `json:"bid"`
	Ask          float64 `json:"ask"`
	SpreadPct    float64 `json:"spread_pct"`
	Volume       int     `json:"volume"`
	OpenInterest int     `json:"open_interest"`
}

// IVContext has forward/spot info
type IVContext struct {
	Spot          float64 `json:"spot"`
	Forward       float64 `json:"forward"`
	ForwardSource string  `json:"forward_source"`
	TTEYears      float64 `json:"time_to_expiry_years"`
}

// SolverSettings controls the solver
type SolverSettings struct {
	VolLow        float64 `json:"vol_low"`
	VolHigh       float64 `json:"vol_high"`
	ToleranceAbs  float64 `json:"tolerance_abs_price"`
	ToleranceRel  float64 `json:"tolerance_rel_price"`
	MaxIterations int     `json:"max_iterations"`
	Method        string  `json:"method"`
}

// IVResult is the output from IV solving
type IVResult struct {
	RequestID   string        `json:"iv_request_id"`
	Status      string        `json:"status"`
	Result      IVResultData  `json:"result"`
	Diagnostics IVDiagnostics `json:"diagnostics"`
	Quality     IVQuality     `json:"quality"`
}

// IVResultData has the solved IV
type IVResultData struct {
	ImpliedVol  float64 `json:"implied_vol"`
	VolUnits    string  `json:"volatility_units"`
	FitErrorAbs float64 `json:"fit_error_abs"`
	FitErrorRel float64 `json:"fit_error_rel"`
	Iterations  int     `json:"iterations"`
	MethodUsed  string  `json:"method_used"`
}

// IVDiagnostics has solver details
type IVDiagnostics struct {
	PriceAtSolution float64   `json:"price_at_solution"`
	VegaAtSolution  float64   `json:"vega_at_solution_per_vol_point"`
	Bracket         IVBracket `json:"bracket"`
	Notes           []string  `json:"notes"`
}

// IVBracket is the bracketing range
type IVBracket struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

// IVQuality has confidence and flags
type IVQuality struct {
	Confidence float64  `json:"confidence"`
	Flags      []string `json:"flags"`
	Warnings   []string `json:"warnings"`
}

// IV Status constants
const (
	IVStatusConverged           = "CONVERGED"
	IVStatusOutOfBounds         = "NO_SOLUTION_MARK_OUT_OF_BOUNDS"
	IVStatusMaxIterations       = "FAILED_MAX_ITERATIONS"
	IVStatusNumeric             = "FAILED_NUMERIC"
	IVStatusSkippedLowLiquidity = "SKIPPED_LOW_LIQUIDITY"
)
