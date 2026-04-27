package app

import (
	"fmt"
	"log"
	"math"

	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/quant/pricing"
)

// LogSnapshotSummary prints high-level stats about the snapshot quality
func LogSnapshotSummary(snap *market.Snapshot) {
	// log.Printf("Merged Snapshot: %s", snap.ID)
	// log.Printf("Underlying: %s, Spot: %.2f", snap.Underlying.Symbol, snap.Underlying.Spot)
	// log.Printf("Total Quotes: %d", len(snap.Quotes))
	// log.Printf("Total Expiries: %d", len(snap.Expiries))
	// log.Printf("Tradable: %d, Rejected: %d", snap.QualitySummary.TradableQuotes, snap.QualitySummary.RejectedQuotes)

	// // Sample quote for sanity check
	// if len(snap.Quotes) > 0 {
	// 	// Just pick the first non-filtered one if possible, or just the first one
	// 	q := snap.Quotes[0]
	// 	log.Printf("Sample Quote: %s %.0f %s | Mid: %.2f | Flags: %v",
	// 		q.Contract.Symbol, q.Contract.Strike, q.Contract.Type, q.Market.Mid, q.Quality.Flags)
	// }
}

// LogCandidates prints detailed recommendations, including Fair Value analysis
func LogCandidates(candidates []market.StrategyCandidate, surface *market.IVSurfaceSnapshot, riskFreeRate float64) {
	log.Println("=== Top Recommendations ===")

	topN := 20
	for i := 0; i < len(candidates) && i < topN; i++ {
		cand := candidates[i]
		log.Printf(" #%d [%s] %s (%s) Score=%.2f | Expiry: %s",
			i+1, cand.StrategyType, cand.ID, cand.Intent, cand.Quality.Score, cand.Expiry)
		log.Printf("    Entry: %s %.2f", cand.Entry.PremiumType, cand.Entry.NetPremium)
		log.Printf("    Rationale:")
		log.Printf("     - Basis: %s", cand.Rationale.SelectionBasis)
		log.Printf("     - Thesis: %s", cand.Rationale.Thesis)
		log.Printf("     - Legs: %s", cand.Rationale.LegSelection)

		// Find the Skew Snapshot for this candidate's expiry to calculate Fair Value
		var candSkew *market.IVSkewSnapshot
		for _, s := range surface.Skews {
			if s.Expiry == cand.Expiry {
				candSkew = &s
				break
			}
		}

		var legs []market.StrategyLeg
		legs = cand.Legs

		for _, leg := range legs {
			fairValue := 0.0
			fvNote := ""

			if candSkew != nil {
				// 1. Get Model IV from Skew (Quadratic Fit)
				// Params are for Total Variance (Vol^2 * T)
				fwd := candSkew.Forward
				if fwd > 0 {
					lm := math.Log(leg.Strike / fwd)
					p := candSkew.Fit.Params
					modelVar := p.A + p.B*lm + p.C*lm*lm

					modelVol := 0.0
					if modelVar > 0 && candSkew.TTEYears > 0 {
						modelVol = math.Sqrt(modelVar / candSkew.TTEYears)
					}

					// 2. Price with Black-Scholes using Model Vol
					tte := candSkew.TTEYears
					r := riskFreeRate

					pCtx := pricing.PricingContext{
						Type:           leg.Type,
						S:              fwd,
						K:              leg.Strike,
						T:              tte,
						R:              r,
						IsForwardModel: true,
						Sigma:          modelVol,
					}
					res := pricing.Calculate(pCtx)
					fairValue = res.Price
					fvNote = fmt.Sprintf("(ModelIV: %.2f%%)", modelVol*100)
				}
			}

			diffPct := 0.0
			if leg.Mark > 0 {
				diffPct = (fairValue - leg.Mark) / leg.Mark * 100
			}

			log.Printf("      - %4s %d %s %.0f @ %.2f | Fair: %.2f %s [Diff: %+.1f%%]",
				leg.Side, leg.Qty, leg.Type, leg.Strike, leg.Mark, fairValue, fvNote, diffPct)
		}
		log.Println("    ---------------------------------------------------")
	}
}
