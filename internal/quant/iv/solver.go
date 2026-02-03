package iv

import (
	"math"

	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/quant/pricing"
)

// Solver implements hybrid Newton-Raphson + Bisection IV solving
type Solver struct {
	Settings market.SolverSettings
}

// DefaultSettings returns standard solver settings
func DefaultSettings() market.SolverSettings {
	return market.SolverSettings{
		VolLow:        0.0001,
		VolHigh:       3.0,
		ToleranceAbs:  0.01,
		ToleranceRel:  0.0001,
		MaxIterations: 50,
		Method:        "HYBRID_NR_BISECTION",
	}
}

// NewSolver creates a solver with given settings
func NewSolver(settings market.SolverSettings) *Solver {
	return &Solver{Settings: settings}
}

// Solve finds implied volatility for the given request
func (s *Solver) Solve(req market.IVRequest) market.IVResult {
	result := market.IVResult{
		RequestID:  req.ID,
		Instrument: req.Instrument,
		Market:     req.Market,
		Quality: market.IVQuality{
			Flags:    []string{},
			Warnings: []string{},
		},
		Diagnostics: market.IVDiagnostics{
			Notes: []string{},
		},
	}

	// Step A: Get mark price
	markPrice := req.Market.MarkPrice
	if markPrice <= 0 {
		result.Status = market.IVStatusNumeric
		result.Quality.Flags = append(result.Quality.Flags, "INVALID_MARK_PRICE")
		return result
	}

	// Step B: Bounds check
	F := req.Context.Forward
	K := req.Instrument.Strike
	T := req.Context.TTEYears
	r := req.Rates.RiskFreeRateCC
	df := req.Rates.DiscountFactor
	if df == 0 {
		df = math.Exp(-r * T)
	}

	var lowerBound, upperBound float64
	if req.Instrument.Type == market.Call {
		lowerBound = df * math.Max(F-K, 0)
		upperBound = df * F
	} else {
		lowerBound = df * math.Max(K-F, 0)
		upperBound = df * K
	}

	if markPrice < lowerBound-0.01 || markPrice > upperBound+0.01 {
		result.Status = market.IVStatusOutOfBounds
		result.Quality.Flags = append(result.Quality.Flags, "MARK_OUT_OF_BOUNDS")
		result.Diagnostics.Notes = append(result.Diagnostics.Notes,
			"Mark price outside theoretical bounds")
		return result
	}

	// Step C: Bracket the volatility
	volLow := s.Settings.VolLow
	volHigh := s.Settings.VolHigh

	priceFn := func(sigma float64) (float64, float64) {
		ctx := pricing.PricingContext{
			Type:           req.Instrument.Type,
			S:              F,
			K:              K,
			T:              T,
			R:              r,
			Q:              0,
			Sigma:          sigma,
			IsForwardModel: true,
		}
		res := pricing.Calculate(ctx)
		return res.Price, res.Greeks.VegaPerVolPoint * 100 // Convert back to gross vega
	}

	priceLow, _ := priceFn(volLow)
	priceHigh, _ := priceFn(volHigh)

	// Ensure bracket contains market price
	if markPrice < priceLow || markPrice > priceHigh {
		// Try expanding bracket
		for volHigh < 5.0 && markPrice > priceHigh {
			volHigh *= 1.5
			priceHigh, _ = priceFn(volHigh)
		}
		if markPrice < priceLow || markPrice > priceHigh {
			result.Status = market.IVStatusOutOfBounds
			result.Quality.Flags = append(result.Quality.Flags, "BRACKET_FAILED")
			return result
		}
	}

	result.Diagnostics.Bracket = market.IVBracket{Low: volLow, High: volHigh}

	// Step D: Hybrid Newton-Raphson + Bisection
	sigma := (volLow + volHigh) / 2.0 // Initial guess: midpoint
	var iterations int
	var price, vega float64
	methodUsed := "BISECTION"

	for iterations < s.Settings.MaxIterations {
		iterations++
		price, vega = priceFn(sigma)
		err := price - markPrice

		// Check convergence
		if math.Abs(err) < s.Settings.ToleranceAbs {
			result.Status = market.IVStatusConverged
			break
		}
		if markPrice > 0 && math.Abs(err/markPrice) < s.Settings.ToleranceRel {
			result.Status = market.IVStatusConverged
			break
		}

		// Newton step if vega is sufficient
		if vega > 0.001 {
			newtonStep := err / vega
			sigmaNew := sigma - newtonStep

			// Check if Newton stays in bracket
			if sigmaNew > volLow && sigmaNew < volHigh {
				sigma = sigmaNew
				methodUsed = "NEWTON"
				// Update bracket
				if err > 0 {
					volHigh = sigma
				} else {
					volLow = sigma
				}
				continue
			}
		}

		// Fallback to bisection
		methodUsed = "BISECTION"
		if err > 0 {
			volHigh = sigma
		} else {
			volLow = sigma
		}
		sigma = (volLow + volHigh) / 2.0
	}

	if result.Status != market.IVStatusConverged {
		result.Status = market.IVStatusMaxIterations
	}

	// Final price/vega at solution
	price, vega = priceFn(sigma)

	result.Result = market.IVResultData{
		ImpliedVol:  sigma,
		VolUnits:    "DECIMAL",
		FitErrorAbs: math.Abs(price - markPrice),
		FitErrorRel: math.Abs(price-markPrice) / markPrice,
		Iterations:  iterations,
		MethodUsed:  methodUsed,
	}

	result.Diagnostics.PriceAtSolution = price
	result.Diagnostics.VegaAtSolution = vega / 100.0 // Per vol point
	result.Diagnostics.Notes = append(result.Diagnostics.Notes,
		"Solved using "+req.Market.MarkSource+" mark")

	// Step E: Confidence scoring
	result.Quality.Confidence = s.calculateConfidence(req, result)

	return result
}

// calculateConfidence computes confidence based on weighted components
func (s *Solver) calculateConfidence(req market.IVRequest, res market.IVResult) float64 {
	// Status check
	if res.Status != market.IVStatusConverged {
		return 0.0
	}

	// Step A: Component Scores
	spreadScore := scoreSpread(req.Market.SpreadPct)
	liqScore := scoreLiquidity(req.Market.Volume, req.Market.OpenInterest, spreadScore)
	markScore := scoreMarkSource(req.Market.MarkSource)
	solverScore := scoreSolverStability(res.Result.Iterations, res.Diagnostics.VegaAtSolution)
	consistencyScore := scoreConsistency(req)

	// Step B: Weighted Combination
	// 45% liquidity + 25% solver + 15% mark + 15% consistency
	confidence := 0.45*liqScore + 0.25*solverScore + 0.15*markScore + 0.15*consistencyScore

	// Clamp 0-1
	if confidence < 0 {
		return 0
	}
	if confidence > 1 {
		return 1
	}
	return confidence
}

// Helper scoring functions

func scoreSpread(spreadPct float64) float64 {
	switch {
	case spreadPct <= 0.02:
		return 1.00
	case spreadPct <= 0.05: // 2-5%
		return 0.80
	case spreadPct <= 0.10: // 5-10%
		return 0.50
	case spreadPct <= 0.15: // 10-15%
		return 0.20
	default: // > 15%
		return 0.00
	}
}

func scoreLiquidity(volume, oi int, spreadScore float64) float64 {
	volScore := 0.3
	if volume >= 1000 {
		volScore = 1.0
	} else if volume > 0 { // Assume logical intermediate step, but sticking to user prompt thresholds
		// User said: else 0.6 else 0.3. Let's assume some intermediate threshold or just default to 0.6 for >0?
		// User: volume >= 1000 -> 1.0 else 0.6 else 0.3
		// Let's interpret "else 0.6" as intermediate volume. Maybe > 100?
		// For strict adherence to "else 0.6" being the middle case, we need a middle threshold.
		// Since none provided, I'll use 0.6 for any volume > 0 and < 1000, and 0.3 for 0.
		volScore = 0.6
	}

	// Refined interpretation based on "else":
	// if volume >= 1000 -> 1.0
	// else (if volume < 1000) -> 0.6? Or is there a lower bound?
	// User prompt: "volume >= 1000 -> 1.0 else 0.6 else 0.3" imply 3 states.
	// likely: >= 1000 -> 1.0, >= something_else -> 0.6, else 0.3.
	// I'll assume 100 as the "something else" for now, or just use 0.6 as fallback for <1000 and 0.3 for 0.
	// Let's implement strict interpretation of "else 0.6" meaning < 1000 but reasonable, and 0.3 meaning very low.
	// I will treat < 100 as very low.
	if volume >= 1000 {
		volScore = 1.0
	} else if volume >= 100 {
		volScore = 0.6
	} else {
		volScore = 0.3
	}

	oiScore := 0.3
	if oi >= 10000 {
		oiScore = 1.0
	} else if oi >= 1000 { // Assuming 1000 as intermediate based on volume ratios
		oiScore = 0.6
	} else {
		oiScore = 0.3
	}

	// Geometric mean
	return math.Sqrt(spreadScore * volScore * oiScore)
}

func scoreMarkSource(source string) float64 {
	switch source {
	case "MID":
		return 1.0
	case "LAST", "LTP":
		return 0.6
	case "MODEL", "MODEL_MARK":
		return 0.4
	default:
		return 0.4 // Missing bid-ask or unknown
	}
}

func scoreSolverStability(iterations int, vega float64) float64 {
	iterScore := 1.0
	if iterations > 20 {
		iterScore = 0.5
	} else if iterations > 10 { // 11-20
		iterScore = 0.8
	}

	vegaScore := 0.3
	if vega >= 2.0 {
		vegaScore = 1.0
	} else if vega >= 0.5 { // 0.5-2.0
		vegaScore = 0.7
	}

	return iterScore * vegaScore
}

func scoreConsistency(req market.IVRequest) float64 {
	// If paired market data is missing, return a neutral/conservative score
	if req.Market.PairedMid <= 0 {
		return 0.7
	}

	K := req.Instrument.Strike
	r := req.Rates.RiskFreeRateCC
	T := req.Context.TTEYears
	F_ref := req.Context.Forward

	// Option prices
	C, P := 0.0, 0.0
	C_bid, P_ask := 0.0, 0.0 // For F_low calculation
	C_ask, P_bid := 0.0, 0.0 // For F_high calculation

	if req.Instrument.Type == market.Call {
		C = req.Market.MarkPrice
		P = req.Market.PairedMid
		C_bid = req.Market.Bid
		C_ask = req.Market.Ask
		P_bid = req.Market.PairedBid
		P_ask = req.Market.PairedAsk
	} else {
		P = req.Market.MarkPrice
		C = req.Market.PairedMid
		P_bid = req.Market.Bid
		P_ask = req.Market.Ask
		C_bid = req.Market.PairedBid
		C_ask = req.Market.PairedAsk
	}

	// 1. Calculate implied forward from Mids: F_implied = K + e^rT * (C - P)
	dfInv := math.Exp(r * T)
	F_implied := K + dfInv*(C-P)

	// Dispersion Metric: |F_impl - F_ref| / F_ref
	if F_ref <= 0 {
		return 0.5 // Should generally not happen if pipeline is correct
	}
	dispersion := math.Abs(F_implied-F_ref) / F_ref

	// Piecewise scoring for dispersion
	dispScore := 0.0
	switch {
	case dispersion <= 0.0005: // <= 0.05%
		dispScore = 1.0
	case dispersion <= 0.0015: // <= 0.15%
		dispScore = 0.85
	case dispersion <= 0.0030: // <= 0.30%
		dispScore = 0.65
	case dispersion <= 0.0060: // <= 0.60%
		dispScore = 0.40
	default:
		dispScore = 0.15
	}

	// 2. Bid-Ask Bounds Check (Strictness)
	// If we have valid bid/ask for both legs, we can verify if F_ref lies within the arbitrage-free band.
	// F_low  = K + e^rT * (C_bid - P_ask)
	// F_high = K + e^rT * (C_ask - P_bid)
	boundsPenalty := 1.0
	if C_bid > 0 && P_ask > 0 && C_ask > 0 && P_bid > 0 {
		F_low := K + dfInv*(C_bid-P_ask)
		F_high := K + dfInv*(C_ask-P_bid)

		// Check if F_ref is within [F_low, F_high]
		if F_ref >= F_low && F_ref <= F_high {
			boundsPenalty = 1.0 // Perfect consistency with market width
		} else {
			// Calculate how far outside it is (relative) - simplified penalty
			// If it's just slightly outside (e.g. within 0.10%), minor penalty
			// If far outside, major penalty
			distLow := F_low - F_ref
			distHigh := F_ref - F_high
			maxDist := 0.0
			if distLow > 0 {
				maxDist = distLow
			} else if distHigh > 0 {
				maxDist = distHigh
			}

			relDist := maxDist / F_ref
			if relDist <= 0.0010 { // 0.10% tolerance
				boundsPenalty = 0.7
			} else {
				boundsPenalty = 0.3
			}
		}
	}

	return dispScore * boundsPenalty
}
