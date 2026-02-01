package surface

import (
	"sort"

	"volatility-engine/internal/domain/market"
)

// BuildTermStructure constructs the term structure from skews
func (b *Builder) BuildTermStructure(skews []market.IVSkewSnapshot) market.TermStructure {
	ts := market.TermStructure{
		Points: []market.TermStructurePoint{},
	}

	// Collect points
	for _, skew := range skews {
		ts.Points = append(ts.Points, market.TermStructurePoint{
			Expiry:     skew.Expiry,
			TTEYears:   skew.TTEYears,
			ATMVol:     skew.Metrics.ATMVol,
			Confidence: skew.Metrics.Confidence,
		})
	}

	// Sort by TTE
	sort.Slice(ts.Points, func(i, j int) bool {
		return ts.Points[i].TTEYears < ts.Points[j].TTEYears
	})

	// Calculate Metrics
	if len(ts.Points) > 0 {
		near := ts.Points[0]
		ts.Metrics.NearExpiry = near.Expiry

		if len(ts.Points) > 1 {
			// Find a "far" point (e.g., next one or significantly further?)
			// Simple prototype: just use the second one, or the last one?
			// User prompt: "nearIV = ATM IV of nearest expiry... farIV = ATM IV of a further expiry"
			// Let's take the last available for maximum range, or just next?
			// Usually adjacent expiries gives better slope for short term.
			// Let's use 2nd point if available for "Front Term Slope".
			far := ts.Points[1] // Use the next expiry for initial slope
			ts.Metrics.FarExpiry = far.Expiry

			dT := far.TTEYears - near.TTEYears
			if dT > 0.0001 {
				ts.Metrics.TermSlope = (far.ATMVol - near.ATMVol) / dT
			}
		}
	}

	return ts
}
