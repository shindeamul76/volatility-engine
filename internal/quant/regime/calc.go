package regime

import (
	"errors"
	"math"
	"sort"

	"volatility-engine/internal/domain/market"
)

const DaysInYear = 365.0

// InterpolateIV30 calculates the 30-day constant maturity IV using variance interpolation.
func InterpolateIV30(skews []market.IVSkewSnapshot) (float64, float64, string, error) {
	// Formula: V30 = V1 + (V2 - V1) * (T30 - T1)/(T2 - T1)
	// sigma30 = sqrt(V30 / T30)

	targetTenor := 30.0 / DaysInYear

	// Filter and sort by TTE
	type point struct {
		iv     float64
		tte    float64
		conf   float64
		expiry string
	}
	var validPoints []point

	for _, s := range skews {
		// Must have valid ATM Vol
		if s.Metrics.ATMVol > 0 {
			validPoints = append(validPoints, point{
				iv:     s.Metrics.ATMVol,
				tte:    s.TTEYears,
				conf:   s.Metrics.Confidence,
				expiry: s.Expiry,
			})
		}
	}

	sort.Slice(validPoints, func(i, j int) bool {
		return validPoints[i].tte < validPoints[j].tte
	})

	if len(validPoints) == 0 {
		return 0, 0, "", errors.New("no valid points for IV30 interpolation")
	}

	// Logic: Find T1 <= T30 <= T2
	var p1, p2 point
	found := false

	if validPoints[0].tte >= targetTenor {
		// All further than 30d
		return validPoints[0].iv, validPoints[0].conf, "NEAREST_LONG_FALLBACK", nil
	}
	if validPoints[len(validPoints)-1].tte <= targetTenor {
		// All shorter than 30d
		last := validPoints[len(validPoints)-1]
		return last.iv, last.conf, "NEAREST_SHORT_FALLBACK", nil
	}

	for i := 0; i < len(validPoints)-1; i++ {
		if validPoints[i].tte <= targetTenor && validPoints[i+1].tte >= targetTenor {
			p1 = validPoints[i]
			p2 = validPoints[i+1]
			found = true
			break
		}
	}

	if !found {
		// Should not happen given bounds checks above
		return 0, 0, "", errors.New("interpolation bracket not found")
	}

	// Variance Interpolation
	v1 := p1.iv * p1.iv * p1.tte
	v2 := p2.iv * p2.iv * p2.tte

	v30 := v1 + (v2-v1)*(targetTenor-p1.tte)/(p2.tte-p1.tte)

	if v30 < 0 {
		return 0, 0, "", errors.New("negative variance in interpolation")
	}

	iv30 := math.Sqrt(v30 / targetTenor)
	w := (targetTenor - p1.tte) / (p2.tte - p1.tte)
	conf30 := p1.conf + (p2.conf-p1.conf)*w

	return iv30, conf30, "VARIANCE_INTERP", nil
}

func ComputeRankFromSeries(current float64, series []float64) float64 {
	if len(series) == 0 {
		return 0.5
	}
	minV, maxV := series[0], series[0]
	for _, v := range series[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if maxV <= minV {
		return 0.5
	}
	r := (current - minV) / (maxV - minV)
	if r < 0 {
		r = 0
	}
	if r > 1 {
		r = 1
	}
	return r
}

func ComputePercentileFromSeries(current float64, series []float64) float64 {
	if len(series) == 0 {
		return 0.5
	}
	// Copy + sort (don’t mutate original)
	s := append([]float64(nil), series...)
	sort.Float64s(s)

	// percentile = fraction <= current
	idx := sort.SearchFloat64s(s, current)
	// SearchFloat64s returns first >= current.
	// For <=, we move to the right across equals.
	for idx < len(s) && s[idx] <= current {
		idx++
	}
	return float64(idx) / float64(len(s))
}

func ComputeHV(closes []float64, tradingDaysPerYear float64) (float64, error) {
	if len(closes) < 2 {
		return 0, errors.New("need at least 2 closes")
	}
	rets := make([]float64, 0, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		if closes[i-1] <= 0 || closes[i] <= 0 {
			continue // Skip invalid prices
		}
		r := math.Log(closes[i] / closes[i-1])
		rets = append(rets, r)
	}
	if len(rets) < 2 {
		return 0, errors.New("not enough valid returns")
	}

	// sample standard deviation
	mean := 0.0
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))

	varSum := 0.0
	for _, r := range rets {
		d := r - mean
		varSum += d * d
	}
	// sample variance: /(n-1)
	variance := varSum / float64(len(rets)-1)

	dailyVol := math.Sqrt(variance)
	annualVol := dailyVol * math.Sqrt(tradingDaysPerYear)
	return annualVol, nil
}
