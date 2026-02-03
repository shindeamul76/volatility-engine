package surface

import (
	"math"
	"sort"

	"volatility-engine/internal/domain/market"
)

// BuildTermStructure constructs the term structure from skews
func (b *Builder) BuildTermStructure(skews []market.IVSkewSnapshot) market.TermStructure {
	ts := market.TermStructure{
		Points: []market.TermStructurePoint{},
	}

	// 1) Collect + filter
	minConf := 0.3 // make configurable
	tmp := make([]market.TermStructurePoint, 0, len(skews))

	for _, skew := range skews {
		if skew.TTEYears <= 0 || skew.Metrics.ATMVol <= 0 {
			continue
		}
		if skew.Metrics.Confidence < minConf {
			continue
		}

		vol := skew.Metrics.ATMVol
		T := skew.TTEYears

		p := market.TermStructurePoint{
			Expiry:     skew.Expiry,
			TTEYears:   T,
			ATMVol:     vol,
			Confidence: skew.Metrics.Confidence,
			// Add this field to struct if possible:
			// ATMVar: vol*vol*T,
		}
		tmp = append(tmp, p)
	}

	// 2) Sort by TTE
	sort.Slice(tmp, func(i, j int) bool {
		return tmp[i].TTEYears < tmp[j].TTEYears
	})

	// 3) De-dup (same expiry or almost-same TTE)
	// Keep higher confidence
	dedup := make([]market.TermStructurePoint, 0, len(tmp))
	const epsT = 1e-6
	for _, p := range tmp {
		n := len(dedup)
		if n == 0 {
			dedup = append(dedup, p)
			continue
		}
		last := dedup[n-1]
		if math.Abs(last.TTEYears-p.TTEYears) < epsT || last.Expiry == p.Expiry {
			// replace if better confidence
			if p.Confidence > last.Confidence {
				dedup[n-1] = p
			}
			continue
		}
		dedup = append(dedup, p)
	}
	ts.Points = dedup

	// 4) Metrics
	if len(ts.Points) == 0 {
		return ts
	}

	near := ts.Points[0]
	ts.Metrics.NearExpiry = near.Expiry

	if len(ts.Points) >= 2 {
		// choose far: next meaningful expiry
		minDT := 5.0 / 365.0
		farIdx := 1
		for i := 1; i < len(ts.Points); i++ {
			if ts.Points[i].TTEYears-near.TTEYears >= minDT {
				farIdx = i
				break
			}
		}
		far := ts.Points[farIdx]
		ts.Metrics.FarExpiry = far.Expiry

		dT := far.TTEYears - near.TTEYears
		if dT > 1e-6 {
			// Prefer variance slope:
			nearVar := near.ATMVol * near.ATMVol * near.TTEYears
			farVar := far.ATMVol * far.ATMVol * far.TTEYears
			ts.Metrics.TermSlope = (farVar - nearVar) / dT // now "variance per year"
		}
	}

	return ts
}
// func (b *Builder) BuildTermStructure(skews []market.IVSkewSnapshot) market.TermStructure {
// 	ts := market.TermStructure{
// 		Points: []market.TermStructurePoint{},
// 	}

// 	// Collect points
// 	for _, skew := range skews {
// 		ts.Points = append(ts.Points, market.TermStructurePoint{
// 			Expiry:     skew.Expiry,
// 			TTEYears:   skew.TTEYears,
// 			ATMVol:     skew.Metrics.ATMVol,
// 			Confidence: skew.Metrics.Confidence,
// 		})
// 	}

// 	// Sort by TTE
// 	sort.Slice(ts.Points, func(i, j int) bool {
// 		return ts.Points[i].TTEYears < ts.Points[j].TTEYears
// 	})

// 	// Calculate Metrics
// 	if len(ts.Points) > 0 {
// 		near := ts.Points[0]
// 		ts.Metrics.NearExpiry = near.Expiry

// 		if len(ts.Points) > 1 {
// 			// Find a "far" point (e.g., next one or significantly further?)
// 			// Simple prototype: just use the second one, or the last one?
// 			// User prompt: "nearIV = ATM IV of nearest expiry... farIV = ATM IV of a further expiry"
// 			// Let's take the last available for maximum range, or just next?
// 			// Usually adjacent expiries gives better slope for short term.
// 			// Let's use 2nd point if available for "Front Term Slope".
// 			far := ts.Points[1] // Use the next expiry for initial slope
// 			ts.Metrics.FarExpiry = far.Expiry

// 			dT := far.TTEYears - near.TTEYears
// 			if dT > 0.0001 {
// 				ts.Metrics.TermSlope = (far.ATMVol - near.ATMVol) / dT
// 			}
// 		}
// 	}
	

// 	return ts
// }
