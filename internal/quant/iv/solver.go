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
		RequestID: req.ID,
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
		return res.Price, res.Greeks.Vega * 100 // Convert back to gross vega
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
					volHigh = sigma + newtonStep*0.5 // Shrink from above
				} else {
					volLow = sigma - newtonStep*0.5 // Shrink from below
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

// calculateConfidence computes confidence based on quote quality and solver stability
func (s *Solver) calculateConfidence(req market.IVRequest, res market.IVResult) float64 {
	conf := 1.0

	// Penalize wide spreads (biggest factor)
	if req.Market.SpreadPct > 0.10 {
		conf -= 0.3
		res.Quality.Warnings = append(res.Quality.Warnings, "Wide spread")
	} else if req.Market.SpreadPct > 0.05 {
		conf -= 0.15
	}

	// Penalize many iterations
	if res.Result.Iterations > 30 {
		conf -= 0.15
	} else if res.Result.Iterations > 15 {
		conf -= 0.05
	}

	// Penalize extreme IV
	if res.Result.ImpliedVol > 1.0 {
		conf -= 0.2
		res.Quality.Warnings = append(res.Quality.Warnings, "Extreme IV")
	}

	// Penalize low vega (unstable)
	if res.Diagnostics.VegaAtSolution < 0.1 {
		conf -= 0.2
		res.Quality.Warnings = append(res.Quality.Warnings, "Low vega")
	}

	// Penalize low liquidity
	if req.Market.Volume < 100 {
		conf -= 0.1
	}
	if req.Market.OpenInterest < 1000 {
		conf -= 0.1
	}

	if conf < 0 {
		conf = 0
	}
	return conf
}
