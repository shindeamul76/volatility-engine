package surface

import (
	"math"
	"sort"

	"volatility-engine/internal/domain/market"
)

// BuildSkew creates a skew for a single expiry
func (b *Builder) BuildSkew(expiry string, points []market.IVPoint, forward, tte float64) market.IVSkewSnapshot {
	skew := market.IVSkewSnapshot{
		// AsOf and Underlying are set by caller usually, but filling what we can
		Expiry:   expiry,
		Forward:  forward,
		TTEYears: tte,
		Points:   []market.IVPoint{},
		Fit: market.SkewFit{
			Type: "QUADRATIC_WLS",
		},
	}

	// 1. Organize points by strike for OTM selection
	byStrike := make(map[float64][]market.IVPoint)
	for _, p := range points {
		byStrike[p.Strike] = append(byStrike[p.Strike], p)
	}

	// 2. Select preferred points (Canonical "one IV per strike")
	var selectedPoints []market.IVPoint
	var strikes []float64
	for k := range byStrike {
		strikes = append(strikes, k)
	}
	sort.Float64s(strikes)

	for _, k := range strikes {
		pts := byStrike[k]
		if len(pts) == 0 {
			continue
		}

		var chosen market.IVPoint

		// If only one point, take it
		if len(pts) == 1 {
			chosen = pts[0]
		} else {
			// Find Call and Put
			var call, put *market.IVPoint
			for i := range pts {
				if pts[i].Type == market.Call {
					call = &pts[i]
				} else if pts[i].Type == market.Put {
					put = &pts[i]
				}
			}

			if call != nil && put != nil && b.settings.OTMPreference {
				// Apply OTM Rule
				if k < forward {
					// Put OTM, Call ITM -> Prefer PUT
					chosen = *put
				} else if k > forward {
					// Call OTM, Put ITM -> Prefer CALL
					chosen = *call
				} else {
					// ATM: Weighted average
					wCall := math.Pow(call.Confidence, b.settings.WeightPower)
					wPut := math.Pow(put.Confidence, b.settings.WeightPower)

					if wCall+wPut > 0 {
						avgIV := (call.ImpliedVol*wCall + put.ImpliedVol*wPut) / (wCall + wPut)
						// Create synthetic ATM point
						chosen = *call // Copy meta from call
						chosen.ImpliedVol = avgIV
						chosen.Confidence = (call.Confidence + put.Confidence) / 2.0
						chosen.Flags = append(chosen.Flags, "ATM_AVERAGE")
					} else {
						// Both low confidence? Pick one with higher conf
						if call.Confidence > put.Confidence {
							chosen = *call
						} else {
							chosen = *put
						}
					}
				}
			} else {
				// Fallback: pick highest confidence
				best := pts[0]
				for _, p := range pts[1:] {
					if p.Confidence > best.Confidence {
						best = p
					}
				}
				chosen = best
			}
		}

		// 3. Normalize: Calculate Log Moneyness x = ln(K/F)
		// Assuming Forward > 0. If not, fallback to 0 log moneyness?
		if forward > 0 {
			chosen.LogMoneyness = math.Log(k / forward)
		} else {
			chosen.LogMoneyness = 0 // Should not happen with valid forward
		}

		// 4. Calculate Fitting Weight
		// w = confidence^p
		chosen.Weight = math.Pow(chosen.Confidence, b.settings.WeightPower)

		selectedPoints = append(selectedPoints, chosen)
	}

	skew.Points = selectedPoints

	// 5. Fit the Curve (Quadratic WLS)
	// Need at least 3 points for quadratic, 2 for linear
	// Filter out very low weight/confidence points for fitting?
	// The settings.ConfidenceMin is already applied in builder, but maybe double check.

	var validPoints []market.IVPoint
	for _, p := range selectedPoints {
		if p.Confidence >= b.settings.ConfidenceMin {
			validPoints = append(validPoints, p)
		}
	}

	fitParams, fitQuality := b.fitQuadratic(validPoints)
	skew.Fit.Params = fitParams
	skew.Fit.Quality = fitQuality

	// 6. Calculate Metrics
	// ATM Vol: fit.a (if valid) or find nearest point
	atmVol := fitParams.A
	if fitQuality.PointsUsed < 2 {
		// Fallback to nearest point
		minAbsX := math.MaxFloat64
		for _, p := range selectedPoints {
			if math.Abs(p.LogMoneyness) < minAbsX {
				minAbsX = math.Abs(p.LogMoneyness)
				atmVol = p.ImpliedVol
			}
		}
	}

	skew.Metrics = market.SkewMetrics{
		ATMVol:     atmVol,
		SkewSlope:  fitParams.B,
		Curvature:  fitParams.C,
		Confidence: 0.0, // Aggregate confidence todo
	}

	// Aggregate confidence logic? Average of points near ATM?
	// Simple prototype: avg confidence of used points
	if len(validPoints) > 0 {
		sumConf := 0.0
		for _, p := range validPoints {
			sumConf += p.Confidence
		}
		skew.Metrics.Confidence = sumConf / float64(len(validPoints))
	}

	return skew
}

// fitQuadratic performs Weighted Least Squares for y = a + bx + cx^2
func (b *Builder) fitQuadratic(points []market.IVPoint) (market.SkewFitParams, market.SkewFitQuality) {
	n := len(points)
	if n < 3 {
		// Fallback to linear or mean?
		// If n=2, fit linear. If n<2, fit simple mean or return 0.
		return b.fitFallback(points)
	}

	// Prepare matrices for Normal Equations: (X^T W X) beta = X^T W Y
	// Matrix size 3x3 for Quadratic

	// Sums needed:
	// sum(w), sum(w*x), sum(w*x^2), sum(w*x^3), sum(w*x^4)
	// sum(w*y), sum(w*y*x), sum(w*y*x^2)

	var sw, swx, swx2, swx3, swx4 float64
	var swy, swyx, swyx2 float64

	for _, p := range points {
		w := p.Weight
		x := p.LogMoneyness
		y := p.ImpliedVol

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

	// Solve linear system 3x3
	// [ sw    swx   swx2 ] [ a ]   [ swy   ]
	// [ swx   swx2  swx3 ] [ b ] = [ swyx  ]
	// [ swx2  swx3  swx4 ] [ c ]   [ swyx2 ]

	// M = ...
	m11, m12, m13 := sw, swx, swx2
	m21, m22, m23 := swx, swx2, swx3
	m31, m32, m33 := swx2, swx3, swx4

	r1, r2, r3 := swy, swyx, swyx2

	// Cramers rule or Gaussian? 3x3 is small enough for direct inverse helper
	det := m11*(m22*m33-m23*m32) - m12*(m21*m33-m23*m31) + m13*(m21*m32-m22*m31)

	if math.Abs(det) < 1e-9 {
		// Singular matrix (collinear points?), fallback
		return b.fitFallback(points)
	}

	invDet := 1.0 / det

	// Solve for a (ATM)
	// Replace col 1 with R
	detA := r1*(m22*m33-m23*m32) - m12*(r2*m33-m23*r3) + m13*(r2*m32-m22*r3)
	a := detA * invDet

	// Solve for b (Slope)
	// Replace col 2 with R
	detB := m11*(r2*m33-m23*r3) - r1*(m21*m33-m23*m31) + m13*(m21*r3-r2*m31)
	slope := detB * invDet

	// Solve for c (Curvature)
	// Replace col 3 with R
	detC := m11*(m22*r3-r2*m32) - m12*(m21*r3-r2*m31) + r1*(m21*m32-m22*m31)
	c := detC * invDet

	// Calculate Weighted RMSE
	sse := 0.0
	for _, p := range points {
		x := p.LogMoneyness
		fitted := a + slope*x + c*x*x
		err := p.ImpliedVol - fitted
		sse += p.Weight * err * err
	}
	rmse := math.Sqrt(sse / sw) // Weighted RMSE

	return market.SkewFitParams{A: a, B: slope, C: c}, market.SkewFitQuality{WeightedRMSE: rmse, PointsUsed: n}
}

func (b *Builder) fitFallback(points []market.IVPoint) (market.SkewFitParams, market.SkewFitQuality) {
	n := len(points)
	if n == 0 {
		return market.SkewFitParams{}, market.SkewFitQuality{}
	}
	if n < 2 {
		// Just return the point as constant line
		return market.SkewFitParams{A: points[0].ImpliedVol}, market.SkewFitQuality{PointsUsed: n}
	}

	// Linear fit WLS
	// y = a + bx
	var sw, swx, swx2 float64
	var swy, swyx float64

	for _, p := range points {
		w := p.Weight
		x := p.LogMoneyness
		y := p.ImpliedVol

		sw += w
		swx += w * x
		swx2 += w * x * x
		swy += w * y
		swyx += w * y * x
	}

	det := sw*swx2 - swx*swx
	if math.Abs(det) < 1e-9 {
		// Average
		return market.SkewFitParams{A: swy / sw}, market.SkewFitQuality{PointsUsed: n}
	}

	a := (swy*swx2 - swx*swyx) / det
	slope := (sw*swyx - swx*swy) / det

	return market.SkewFitParams{A: a, B: slope, C: 0}, market.SkewFitQuality{PointsUsed: n}
}
