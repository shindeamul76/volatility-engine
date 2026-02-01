package regime

import (
	"errors"
	"math"
	"sort"

	"volatility-engine/internal/domain/market"
)

const DaysInYear = 365.0

// InterpolateIV30 calculates the 30-day constant maturity IV using variance interpolation.
func InterpolateIV30(skews []market.IVSkewSnapshot) (float64, float64, error) {
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
		// Edge case: TTE < 2 days -> Skip or handle?
		// User: "if T < 2 days, use next expiry"
		// We'll filter them out from "valid points" for interpolation unless it's the only ones we have?
		// Better: filter list first.
		if s.TTEYears*DaysInYear < 2.0 {
			continue
		}
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
		return 0, 0, errors.New("no valid points for IV30 interpolation")
	}

	// Logic: Find T1 <= T30 <= T2
	var p1, p2 point
	found := false

	// If nearest is already > 30 days (T1 > T30), extrapolate backward or just use T1?
	// Interpolation requires bracketing.
	// Common practice: if T1 > T30, use T1. If TN < T30, use TN.
	// Or extrapolate. Variance interp works for extrapolation too, but risky.
	// Let's stick to bracketing or nearest.

	if validPoints[0].tte >= targetTenor {
		// All further than 30d
		return validPoints[0].iv, validPoints[0].conf, nil
	}
	if validPoints[len(validPoints)-1].tte <= targetTenor {
		// All shorter than 30d
		last := validPoints[len(validPoints)-1]
		return last.iv, last.conf, nil
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
		return 0, 0, errors.New("interpolation bracket not found")
	}

	// Variance Interpolation
	v1 := p1.iv * p1.iv * p1.tte
	v2 := p2.iv * p2.iv * p2.tte

	v30 := v1 + (v2-v1)*(targetTenor-p1.tte)/(p2.tte-p1.tte)

	if v30 < 0 {
		return 0, 0, errors.New("negative variance in interpolation")
	}

	iv30 := math.Sqrt(v30 / targetTenor)

	// Weighted Confidence
	// Simple linear weighting by time proximity? Or user's "penalize if one side is low confidence"?
	// Let's use time-weighted average confidence
	w := (targetTenor - p1.tte) / (p2.tte - p1.tte) // 0 at p1, 1 at p2
	// Verify: if target == p1, w=0. Correct?
	// No: if target=p1, we want full p1.
	// Linear interp: y = y1 + slope*(x-x1)
	// conf = conf1 + (conf2-conf1)*w

	conf30 := p1.conf + (p2.conf-p1.conf)*w

	return iv30, conf30, nil
}

func ComputeRank(current, min, max float64) float64 {
	if max <= min {
		return 0.5 // Insufficient range
	}
	rank := (current - min) / (max - min)
	return math.Max(0.0, math.Min(1.0, rank))
}

func ComputePercentile(current float64, history []float64) float64 {
	if len(history) == 0 {
		return 0.5
	}
	count := 0
	for _, v := range history {
		if v <= current {
			count++
		}
	}
	return float64(count) / float64(len(history))
}
