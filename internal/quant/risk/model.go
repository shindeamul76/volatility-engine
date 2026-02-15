package risk

import (
	"math"

	"volatility-engine/internal/domain/market"
)

// BSM calculates Black-Scholes-Merton price and greeks.
// T is time to expiry in years.
// r is risk-free rate (continuous).
// sigma is volatility (decimal).
type BSMResult struct {
	Price float64
	Delta float64
	Gamma float64
	Theta float64
	Vega  float64
	Rho   float64
}

func BSM(isCall bool, S, K, T, r, sigma float64) BSMResult {
	if T < market.MinTTEYears {
		intrinsic := 0.0
		if isCall {
			intrinsic = math.Max(0, S-K)
		} else {
			intrinsic = math.Max(0, K-S)
		}
		return BSMResult{Price: intrinsic, Delta: 0, Gamma: 0, Theta: 0, Vega: 0, Rho: 0}
	}

	d1 := (math.Log(S/K) + (r+0.5*sigma*sigma)*T) / (sigma * math.Sqrt(T))
	d2 := d1 - sigma*math.Sqrt(T)

	nd1 := normCdf(d1)
	nd2 := normCdf(d2)
	npd1 := normPdf(d1)

	var price, delta, theta, rho float64
	gamma := npd1 / (S * sigma * math.Sqrt(T))
	vega := S * npd1 * math.Sqrt(T) / 100.0 // Vega is usually quoted per 1% change, so /100

	if isCall {
		price = S*nd1 - K*math.Exp(-r*T)*nd2
		delta = nd1
		theta = (-(S * sigma * npd1) / (2 * math.Sqrt(T))) - (r * K * math.Exp(-r*T) * nd2)
		rho = K * T * math.Exp(-r*T) * nd2 / 100.0 // Rho per 1% rate change? Standard definition is just unit. Let's stick to standard and verify scaling later. Actually usually /100.
	} else {
		// Put
		price = K*math.Exp(-r*T)*normCdf(-d2) - S*normCdf(-d1)
		delta = nd1 - 1
		theta = (-(S * sigma * npd1) / (2 * math.Sqrt(T))) + (r * K * math.Exp(-r*T) * normCdf(-d2))
		rho = -K * T * math.Exp(-r*T) * normCdf(-d2) / 100.0
	}

	theta = theta / market.DaysPerYear // Theta per day (Standardized to 365)

	return BSMResult{
		Price: price,
		Delta: delta,
		Gamma: gamma,
		Theta: theta,
		Vega:  vega,
		Rho:   rho,
	}
}

// BSMForward calculates Black-Scholes-Merton price and greeks using forward-based pricing.
// This form naturally handles dividend yield (q) through the forward price.
// F = forward price (already accounts for carry: F = S * exp((r-q)T))
// DF = discount factor = exp(-rT)
// T is time to expiry in years.
// sigma is volatility (decimal).
func BSMForward(isCall bool, F, K, DF, T, sigma float64) BSMResult {
	if T < market.MinTTEYears {
		var intrinsic float64
		if isCall {
			intrinsic = math.Max(0, F-K) * DF
		} else {
			intrinsic = math.Max(0, K-F) * DF
		}
		return BSMResult{Price: intrinsic, Delta: 0, Gamma: 0, Theta: 0, Vega: 0, Rho: 0}
	}

	// d1 and d2 using forward-based formula
	d1 := (math.Log(F/K) + 0.5*sigma*sigma*T) / (sigma * math.Sqrt(T))
	d2 := d1 - sigma*math.Sqrt(T)

	nd1 := normCdf(d1)
	nd2 := normCdf(d2)
	npd1 := normPdf(d1)

	var price, delta float64

	if isCall {
		price = DF * (F*nd1 - K*nd2)
		delta = DF * nd1 // Delta w.r.t. forward
	} else {
		price = DF * (K*normCdf(-d2) - F*normCdf(-d1))
		delta = -DF * normCdf(-d1) // Delta w.r.t. forward
	}

	// Gamma (w.r.t. forward)
	gamma := DF * npd1 / (F * sigma * math.Sqrt(T))

	// Vega (same formula, scaled for per-vol-point)
	vega := DF * F * npd1 * math.Sqrt(T) / 100.0

	// Theta (per year, then divide by 365)
	// For forward pricing: theta primarily from discounting
	r := -math.Log(DF) / T // Implied rate from DF
	var theta float64
	if isCall {
		theta = (-(DF * F * sigma * npd1) / (2 * math.Sqrt(T))) - (r * K * DF * nd2)
	} else {
		theta = (-(DF * F * sigma * npd1) / (2 * math.Sqrt(T))) + (r * K * DF * normCdf(-d2))
	}
	theta = theta / market.DaysPerYear // Per day

	// Rho (per 1% rate change)
	rho := K * T * DF * nd2 / 100.0
	if !isCall {
		rho = -K * T * DF * normCdf(-d2) / 100.0
	}

	return BSMResult{
		Price: price,
		Delta: delta,
		Gamma: gamma,
		Theta: theta,
		Vega:  vega,
		Rho:   rho,
	}
}

// Standard Normal CDF
func normCdf(x float64) float64 {
	return 0.5 * (1 + math.Erf(x/math.Sqrt2))
}

// Standard Normal PDF
func normPdf(x float64) float64 {
	return (1 / math.Sqrt(2*math.Pi)) * math.Exp(-0.5*x*x)
}

// --- Carry & Forward Models ---

// CalculateCarry computes the implied carry rate (r - q).
// F = S * exp(carry * T) => carry = ln(F/S) / T
func CalculateCarry(spot, forward, tte float64) float64 {
	if tte <= 0.0001 {
		return 0 // Avoid division by zero near expiry
	}
	return math.Log(forward/spot) / tte
}

// CalculateForward projects spot to forward using carry.
// F = S * exp(carry * T)
func CalculateForward(spot, carry, tte float64) float64 {
	return spot * math.Exp(carry*tte)
}

// --- Volatility Model ---

// VolFromSkew calculates implied volatility for a strike using a quadratic model on log-moneyness.
// Model: sigma(x) = A + Bx + Cx^2
// where x = ln(K / F)
// params: [A, B, C]
func VolFromSkew(params []float64, strike, forward, tteReference float64) float64 {
	if len(params) < 3 {
		return 0
	}
	if tteReference <= 0 {
		return 0 // Avoid division by zero
	}
	if forward <= 0 || strike <= 0 {
		// Fallback to ATM vol
		variance := params[0]
		if variance < 0 {
			variance = 0
		}
		return math.Sqrt(variance / tteReference)
	}

	x := math.Log(strike / forward)
	variance := params[0] + params[1]*x + params[2]*x*x

	// Safety clamp
	if variance < 0 {
		variance = 0 // Variance cannot be negative
	}

	vol := math.Sqrt(variance / tteReference)

	if vol < 0.01 {
		return 0.01
	}
	if vol > 5.0 {
		return 5.0
	}

	return vol
}
