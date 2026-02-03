package surface

import (
	"math"
	"sort"

	"volatility-engine/internal/domain/market"
)

// BuildSkew creates a skew for a single expiry
func (b *Builder) BuildSkew(expiry string, points []market.IVPoint, forward, tte float64) market.IVSkewSnapshot {
	skew := market.IVSkewSnapshot{
		Expiry:   expiry,
		Forward:  forward,
		TTEYears: tte,
		Points:   []market.IVPoint{},
		Fit: market.SkewFit{
			Type: "QUADRATIC_VARIANCE",
		},
	}

	if forward <= 0 || tte <= 0 {
		return skew // Cannot build without F and TTE
	}

	// 1. Group by strike (Integer Key)
	byStrike := make(map[int][]market.IVPoint)
	for _, p := range points {
		kInt := int(math.Round(p.Strike))
		byStrike[kInt] = append(byStrike[kInt], p)
	}

	keys := make([]int, 0, len(byStrike))
	for k := range byStrike {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	var selectedPoints []market.IVPoint

	// 2. Select Preferred Points
	for _, kInt := range keys {
		pts := byStrike[kInt]
		if len(pts) == 0 {
			continue
		}
		strike := float64(kInt)

		var chosen market.IVPoint

		if len(pts) == 1 {
			chosen = pts[0]
			// Drop ITM-only if configured
			if b.settings.OTMPreference && b.settings.DropITMOnly {
				if !isATM(strike, forward, b.settings.ATMBandX) && !isOTM(chosen.Type, strike, forward) {
					continue
				}
			}
		} else {
			// Find Call/Put
			var call, put *market.IVPoint
			for i := range pts {
				if pts[i].Type == market.Call {
					call = &pts[i]
				} else if pts[i].Type == market.Put {
					put = &pts[i]
				}
			}

			if call != nil && put != nil && b.settings.OTMPreference {
				if isATM(strike, forward, b.settings.ATMBandX) {
					// ATM Band: Blend
					chosen = blendATM(*call, *put, b.settings.WeightPowerConf)
				} else if strike < forward {
					chosen = *put // OTM Put
				} else {
					chosen = *call // OTM Call
				}
			} else {
				// Fallback: Best confidence
				best := pts[0]
				for _, p := range pts[1:] {
					if p.Confidence > best.Confidence {
						best = p
					}
				}
				chosen = best
			}
		}

		// 3. Pre-Filter check (before weight calc, saves processing)
		// Need LogMoneyness for filter
		chosen.LogMoneyness = math.Log(strike / forward)

		ok, _ := b.acceptPoint(chosen, forward)
		if !ok {
			// Maybe log valid but rejected points?
			continue
		}

		// 4. Calculate Weight
		chosen.Weight = b.computeWeight(chosen)

		selectedPoints = append(selectedPoints, chosen)
	}

	skew.Points = selectedPoints

	// 5. Fit Variance Curve (Total Variance = IV^2 * T)
	// We only fit on points that passed filters (ConfidenceMin is checked in acceptPoint)
	fitParams, fitQuality := b.fitVarianceQuadratic(selectedPoints, tte)
	skew.Fit.Params = fitParams
	skew.Fit.Quality = fitQuality

	// 6. Metrics
	// ATM Vol from fit: sqrt(A / TTE)
	atmVar := fitParams.A
	atmVol := 0.0
	if atmVar > 0 {
		atmVol = math.Sqrt(atmVar / tte)
	} else {
		// Fallback if fit failed or negative variance
		atmVol = fallbackNearestATMIV(selectedPoints)
	}

	// Check Relative RMSE
	if atmVol > 0 {
		relRMSE := fitQuality.WeightedRMSE / atmVol // RMSE is in Variance units? No, let's CHECK fitVarianceQuadratic
		// If fitVarianceQuadratic returns RMSE in variance units, we should ideally convert or compare appropriately.
		// Let's assume WeightedRMSE is in same units as Y (Variance).
		// Relative Error in Variance ~ 2 * Relative Error in Vol?
		// Let's flag if high.
		if relRMSE > 0.15*atmVol { // Heuristic
			skew.Fit.Quality.Flags = append(skew.Fit.Quality.Flags, "FIT_RMSE_HIGH")
		}
	}

	skew.Metrics = market.SkewMetrics{
		ATMVol:     atmVol,
		SkewSlope:  fitParams.B,             // Variance Slope
		Curvature:  fitParams.C,             // Variance Curvature
		Confidence: fitQuality.WeightedRMSE, // Reuse field or calc average conf
	}

	if len(selectedPoints) > 0 {
		sumConf := 0.0
		for _, p := range selectedPoints {
			sumConf += p.Confidence
		}
		skew.Metrics.Confidence = sumConf / float64(len(selectedPoints))
	}

	return skew
}

// Helpers

func isOTM(optType market.OptionType, strike, forward float64) bool {
	if optType == market.Put {
		return strike < forward
	}
	return strike > forward
}

func isATM(strike, forward, atmBandX float64) bool {
	x := math.Log(strike / forward)
	return math.Abs(x) <= atmBandX
}

func blendATM(call, put market.IVPoint, pConf float64) market.IVPoint {
	wC := math.Pow(call.Confidence, pConf)
	wP := math.Pow(put.Confidence, pConf)
	if wC+wP <= 0 {
		if call.Confidence >= put.Confidence {
			return call
		}
		return put
	}

	out := call
	out.ImpliedVol = (call.ImpliedVol*wC + put.ImpliedVol*wP) / (wC + wP)
	out.Confidence = (call.Confidence + put.Confidence) / 2.0
	out.Flags = append(out.Flags, "ATM_AVERAGE")
	return out
}

func (b *Builder) acceptPoint(p market.IVPoint, forward float64) (bool, string) {
	if forward <= 0 {
		return false, "BAD_FORWARD"
	}
	if p.Confidence < b.settings.ConfidenceMin {
		return false, "LOW_CONF"
	}
	if p.ImpliedVol < b.settings.IVMin || p.ImpliedVol > b.settings.IVMax {
		return false, "IV_OUT_OF_RANGE"
	}
	if math.Abs(p.LogMoneyness) > b.settings.XMax {
		return false, "OUTSIDE_X_WINDOW"
	}
	if b.settings.MinMarkPrice > 0 && p.MarkPrice > 0 && p.MarkPrice < b.settings.MinMarkPrice {
		return false, "LOW_PREMIUM"
	}
	if b.settings.MaxFitErrorAbs > 0 && p.FitErrorAbs > b.settings.MaxFitErrorAbs {
		return false, "HIGH_FIT_ERROR"
	}
	if b.settings.MinVegaPerVolPoint > 0 && p.VegaPerPoint > 0 && p.VegaPerPoint < b.settings.MinVegaPerVolPoint {
		return false, "LOW_VEGA"
	}
	return true, ""
}

func (b *Builder) computeWeight(p market.IVPoint) float64 {
	conf := math.Max(0.0, math.Min(1.0, p.Confidence))
	wConf := math.Pow(conf, b.settings.WeightPowerConf)

	v := p.VegaPerPoint
	if v <= 0 {
		v = 1.0 // fallback
	}
	wVega := math.Pow(v, b.settings.WeightPowerVega)

	taper := 1.0
	if b.settings.TaperX0 > 0 {
		ax := math.Abs(p.LogMoneyness)
		// Gaussian taper: exp(-(x/x0)^2)
		taper = math.Exp(-math.Pow(ax/b.settings.TaperX0, 2))
	}

	w := wConf * wVega * taper
	if w < 1e-9 {
		w = 0
	}
	return w
}

func fallbackNearestATMIV(points []market.IVPoint) float64 {
	if len(points) == 0 {
		return 0
	}
	minAbsX := math.MaxFloat64
	val := 0.0
	for _, p := range points {
		if math.Abs(p.LogMoneyness) < minAbsX {
			minAbsX = math.Abs(p.LogMoneyness)
			val = p.ImpliedVol
		}
	}
	return val
}

// fitVarianceQuadratic fits w(x) = a + bx + cx^2 where y = TotalVariance = \sigma^2 * T
func (b *Builder) fitVarianceQuadratic(points []market.IVPoint, tte float64) (market.SkewFitParams, market.SkewFitQuality) {
	n := len(points)
	if n < 3 {
		return b.fitFallback(points, tte)
	}

	var sw, swx, swx2, swx3, swx4 float64
	var swy, swyx, swyx2 float64
	sse := 0.0

	for _, p := range points {
		w := p.Weight
		x := p.LogMoneyness
		// Y = Total Variance
		y := p.ImpliedVol * p.ImpliedVol * tte

		x2 := x * x
		x3 := x2 * x
		x4 := x3 * x

		sw += w
		swx += w * x
		swx2 += w * x2
		swx3 += w * x3
		swx4 += w * x4

		swy += w * y
		swyx += w * y * x
		swyx2 += w * y * x2
	}

	// 3x3 Matrix
	A := [3][3]float64{
		{sw, swx, swx2},
		{swx, swx2, swx3},
		{swx2, swx3, swx4},
	}
	rhs := [3]float64{swy, swyx, swyx2}

	sol, ok := solve3x3(A, rhs)

	if !ok {
		// Singular
		return b.fitFallback(points, tte)
	}

	a, slope, c := sol[0], sol[1], sol[2]

	// Clamp negative variance for a (ATM)
	if a < 0 {
		a = 0
	}

	// Calculate RMSE (in Variance units) and Count Meaningful Points
	pointsUsed := 0
	for _, p := range points {
		x := p.LogMoneyness
		y := p.ImpliedVol * p.ImpliedVol * tte
		fitted := a + slope*x + c*x*x
		err := y - fitted
		sse += p.Weight * err * err

		if p.Weight > 0.05 {
			pointsUsed++
		}
	}
	rmse := 0.0
	if sw > 0 {
		rmse = math.Sqrt(sse / sw)
	}

	return market.SkewFitParams{A: a, B: slope, C: c}, market.SkewFitQuality{WeightedRMSE: rmse, PointsUsed: pointsUsed}
}

func (b *Builder) fitFallback(points []market.IVPoint, tte float64) (market.SkewFitParams, market.SkewFitQuality) {
	// Simple ATM average
	n := len(points)
	if n == 0 {
		return market.SkewFitParams{}, market.SkewFitQuality{Flags: []string{"NO_POINTS"}}
	}

	// Just weighted mean of variance
	sw := 0.0
	swy := 0.0
	for _, p := range points {
		w := p.Weight
		y := p.ImpliedVol * p.ImpliedVol * tte
		sw += w
		swy += w * y
	}

	if sw <= 0 {
		return market.SkewFitParams{A: points[0].ImpliedVol * points[0].ImpliedVol * tte}, market.SkewFitQuality{PointsUsed: n, Flags: []string{"ZERO_WEIGHT"}}
	}

	a := swy / sw
	return market.SkewFitParams{A: a, B: 0, C: 0}, market.SkewFitQuality{PointsUsed: n, Flags: []string{"FIT_FALLBACK_MEAN"}}
}

// Gaussian elimination for 3x3
func solve3x3(A [3][3]float64, b [3]float64) (x [3]float64, ok bool) {
	M := [3][4]float64{
		{A[0][0], A[0][1], A[0][2], b[0]},
		{A[1][0], A[1][1], A[1][2], b[1]},
		{A[2][0], A[2][1], A[2][2], b[2]},
	}

	// Forward elimination with pivoting
	for i := 0; i < 3; i++ {
		pivot := i
		for r := i + 1; r < 3; r++ {
			if math.Abs(M[r][i]) > math.Abs(M[pivot][i]) {
				pivot = r
			}
		}
		if math.Abs(M[pivot][i]) < 1e-12 {
			return x, false
		}
		M[i], M[pivot] = M[pivot], M[i]

		for r := i + 1; r < 3; r++ {
			f := M[r][i] / M[i][i]
			for c := i; c < 4; c++ {
				M[r][c] -= f * M[i][c]
			}
		}
	}

	// Back substitution
	for i := 2; i >= 0; i-- {
		sum := M[i][3]
		for c := i + 1; c < 3; c++ {
			sum -= M[i][c] * x[c]
		}
		x[i] = sum / M[i][i]
	}
	return x, true
}
