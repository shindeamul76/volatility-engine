package pricing

import (
	"math"

	"volatility-engine/internal/domain/market"
)

// PricingContext holds all inputs required for pricing an option.
// It supports both Spot and Forward models via IsForwardModel flag.
type PricingContext struct {
	Type           market.OptionType
	S              float64 // Spot price (or Forward price if IsForwardModel is true)
	K              float64 // Strike price
	T              float64 // Time to expiry (in years)
	R              float64 // Risk-free rate (annualized continuous)
	Q              float64 // Dividend yield / Carry (annualized continuous)
	Sigma          float64 // Volatility (annualized)
	IsForwardModel bool    // If true, S is treated as Forward Price F, and drift is just discount.
}

// Greeks definition moved to market package

// Intermediates holds d1, d2, nd1, nd2 for diagnostics
type Intermediates struct {
	D1  float64
	D2  float64
	Nd1 float64
	Nd2 float64
}

// Result bundles price and greeks.
type Result struct {
	Price float64
	market.Greeks
	Intermediates
	Quality Quality
}

// Quality holds pricing quality flags
type Quality struct {
	OK       bool
	Flags    []string
	Warnings []string
}

// Constants for edge cases
const (
	SmallT     = 1e-6
	SmallSigma = 1e-6
)

// Calculate computes the Black-Scholes price and Greeks.
func Calculate(ctx PricingContext) Result {
	// Handle Edge Cases
	if ctx.T < SmallT {
		return priceNearExpiry(ctx)
	}
	if ctx.Sigma < SmallSigma {
		return priceLowVol(ctx)
	}

	d1, d2 := calculateD1D2(ctx)
	return calculateBS(ctx, d1, d2)
}

func calculateD1D2(ctx PricingContext) (float64, float64) {
	// Forward Price F
	// If IsForwardModel, S is F already. R is discount rate.
	// If Spot Model, F = S * e^{(r-q)T}

	var F float64

	// log.Println("IsForwardModel: ", ctx.IsForwardModel)
	// log.Println("S: ", ctx.S)
	// log.Println("R: ", ctx.R)
	// log.Println("Q: ", ctx.Q)
	// log.Println("T: ", ctx.T)
	// log.Println("K: ", ctx.K)
	// log.Println("Sigma: ", ctx.Sigma)
	if ctx.IsForwardModel {
		F = ctx.S
	} else {
		F = ctx.S * math.Exp((ctx.R-ctx.Q)*ctx.T)
	}

	// log.Println("F: ", F)

	// d1 = (ln(F/K) + 0.5 * sigma^2 * T) / (sigma * sqrt(T))
	// d2 = d1 - sigma * sqrt(T)

	sqrtT := math.Sqrt(ctx.T)
	volT := ctx.Sigma * sqrtT

	d1 := (math.Log(F/ctx.K) + 0.5*ctx.Sigma*ctx.Sigma*ctx.T) / volT
	d2 := d1 - volT

	return d1, d2
}

func calculateBS(ctx PricingContext, d1, d2 float64) Result {
	var price, delta, gamma, vega, theta float64

	sqrtT := math.Sqrt(ctx.T)
	expRT := math.Exp(-ctx.R * ctx.T) // Discount factor DF

	// For greeks, we need Spot S even in Forward model?
	// Usually Greeks are with respect to Spot.
	// We will assume standard Greeks definitions.
	// If IsForwardModel, we might need to back out Spot if we want Spot Delta.
	// F = S * e^(r-q)T => S = F * e^{-(r-q)T}

	var S float64
	var qDiscount float64 // e^{-qT} for Spot model term

	if ctx.IsForwardModel {
		S = ctx.S * math.Exp(-(ctx.R-ctx.Q)*ctx.T)
		// Effectively treat q = r - (ln(F/S)/T) ?
		// Simpler: Use the standard Forward formulations for Greeks.
		// Delta(Forward) vs Delta(Spot). User requested BS variants.
		// Let's stick to standard Spot Delta.
		qDiscount = math.Exp(-ctx.Q * ctx.T)
	} else {
		S = ctx.S
		qDiscount = math.Exp(-ctx.Q * ctx.T)
	}

	nd1 := normCDF(d1)
	nd2 := normCDF(d2)
	npd1 := normPDF(d1) // n(d1)

	if ctx.Type == market.Call {
		// C = S * e^{-qT} * N(d1) - K * e^{-rT} * N(d2)
		price = S*qDiscount*nd1 - ctx.K*expRT*nd2

		// Delta = e^{-qT} * N(d1)
		delta = qDiscount * nd1

		// Theta (Call) - usually negative
		// Using standard textbook formula (Hull)
		// Theta = - (S * n(d1) * sigma * e^{-qT}) / (2 * sqrt(T))
		//         + q * S * N(d1) * e^{-qT}
		//         - r * K * e^{-rT} * N(d2)

		term1 := -(S * npd1 * ctx.Sigma * qDiscount) / (2 * sqrtT)
		term2 := ctx.Q * S * nd1 * qDiscount
		term3 := ctx.R * ctx.K * expRT * nd2
		theta = term1 + term2 - term3

	} else {
		// Put
		n_d1 := normCDF(-d1)
		n_d2 := normCDF(-d2)

		// P = K * e^{-rT} * N(-d2) - S * e^{-qT} * N(-d1)
		price = ctx.K*expRT*n_d2 - S*qDiscount*n_d1

		// Delta = e^{-qT} * (N(d1) - 1)
		delta = qDiscount * (nd1 - 1)

		// Theta (Put)
		// Theta = - (S * n(d1) * sigma * e^{-qT}) / (2 * sqrt(T))
		//         - q * S * N(-d1) * e^{-qT}
		//         + r * K * e^{-rT} * N(-d2)

		term1 := -(S * npd1 * ctx.Sigma * qDiscount) / (2 * sqrtT)
		term2 := ctx.Q * S * n_d1 * qDiscount
		term3 := ctx.R * ctx.K * expRT * n_d2
		theta = term1 - term2 + term3
	}

	// Gamma (Same for Call and Put)
	// Gamma = (n(d1) * e^{-qT}) / (S * sigma * sqrt(T))
	gamma = (npd1 * qDiscount) / (S * ctx.Sigma * sqrtT)

	// Vega (Same for Call and Put)
	// Vega = S * sqrt(T) * n(d1) * e^{-qT}
	// This is Vega for 100% vol change (sigma goes 0.2 -> 1.2).
	// User wants per 1 vol point (0.01).
	grossVega := S * sqrtT * npd1 * qDiscount
	vega = grossVega / 100.0

	// Theta conversion: per day
	theta = theta / 365.0

	return Result{
		Price: price,
		Greeks: market.Greeks{
			Delta:           delta,
			Gamma:           gamma,
			VegaPerVolPoint: vega,
			ThetaPerDay:     theta,
		},
		Intermediates: Intermediates{
			D1:  d1,
			D2:  d2,
			Nd1: nd1,
			Nd2: nd2,
		},
		Quality: Quality{
			OK:       true,
			Flags:    []string{},
			Warnings: []string{},
		},
	}
}

// Edge Case: Near Expiry (T -> 0)
func priceNearExpiry(ctx PricingContext) Result {
	// Intrinsic Value
	var intrinsic float64
	var S float64

	if ctx.IsForwardModel {
		S = ctx.S // F approx S at exp? F converges to S.
	} else {
		S = ctx.S
	}

	if ctx.Type == market.Call {
		intrinsic = math.Max(0, S-ctx.K)
	} else {
		intrinsic = math.Max(0, ctx.K-S)
	}

	// Delta: 0 or 1 (or -1) dependent on ITM
	delta := 0.0
	if ctx.Type == market.Call {
		if S > ctx.K {
			delta = 1.0
		}
	} else {
		if S < ctx.K {
			delta = -1.0
		}
	}

	// Gamma explodes at ATM, 0 elsewhere
	// Vega -> 0
	// Theta -> 0 or huge? (Time decay is max at exp) - let's return 0 for safe fallback or flag it.

	return Result{
		Price: intrinsic,
		Greeks: market.Greeks{
			Delta:           delta,
			Gamma:           0, // Unstable
			VegaPerVolPoint: 0,
			ThetaPerDay:     0, // Unstable
		},
		Quality: Quality{
			OK:       true,
			Flags:    []string{"NEAR_EXPIRY"},
			Warnings: []string{"Greeks may be unstable"},
		},
	}
}

// Edge Case: Low Vol (Sigma -> 0)
func priceLowVol(ctx PricingContext) Result {
	// Discounted Intrinsic
	// Similar to T->0 but we discount K and S (if dividend)

	expRT := math.Exp(-ctx.R * ctx.T)
	var S float64
	var qDiscount float64
	if ctx.IsForwardModel {
		S = ctx.S * math.Exp(-(ctx.R-ctx.Q)*ctx.T)
		qDiscount = math.Exp(-ctx.Q * ctx.T)
	} else {
		S = ctx.S
		qDiscount = math.Exp(-ctx.Q * ctx.T)
	}

	var price float64
	var delta float64

	if ctx.Type == market.Call {
		price = math.Max(0, S*qDiscount-ctx.K*expRT)
		if S*qDiscount > ctx.K*expRT {
			delta = qDiscount // e^{-qT}
		}
	} else {
		price = math.Max(0, ctx.K*expRT-S*qDiscount)
		if ctx.K*expRT > S*qDiscount {
			delta = -qDiscount
		}
	}

	return Result{
		Price: price,
		Greeks: market.Greeks{
			Delta:           delta,
			Gamma:           0,
			VegaPerVolPoint: 0,
			ThetaPerDay:     0, // Only interest/carry theta
		},
		Quality: Quality{
			OK:       true,
			Flags:    []string{"LOW_VOLATILITY"},
			Warnings: []string{"Vol near zero"},
		},
	}
}

// Math Helpers

func normCDF(x float64) float64 {
	return 0.5 * (1 + math.Erf(x/math.Sqrt2))
}

func normPDF(x float64) float64 {
	return math.Exp(-0.5*x*x) / math.Sqrt(2*math.Pi)
}
