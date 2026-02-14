package intel

import (
	"fmt"
	"math"
	"sort"

	"volatility-engine/internal/domain/market"
)

type Engine struct {
	// Config can go here
}

func NewEngine() *Engine {
	return &Engine{}
}

// ComputeIntel generates intelligence for a specific expiry chain.
func (e *Engine) ComputeIntel(chain *market.ChainSnapshot, surface *market.IVSurfaceSnapshot) *market.ChainIntelSnapshot {
	out := &market.ChainIntelSnapshot{
		Expiry: chain.Expiry.Expiry.Format("2006-01-02"),
	}

	// 1. Filter Eligible Strikes
	// We use the same eligibility, but we need the objects.
	eligibleStrikes := filterEligible(chain)
	nearATMStrikes := filterNearATM(chain, eligibleStrikes)

	// 2. PCR
	out.PCR = computePCR(eligibleStrikes, nearATMStrikes)

	// 3. OI Walls
	out.OIWalls = computeOIWalls(eligibleStrikes, chain.Underlying.Spot)

	// 4. Max Pain
	out.MaxPain = computeMaxPain(eligibleStrikes)

	// 5. Pin Risk (only if near expiry)
	out.PinRisk = computePinRisk(eligibleStrikes, chain.Underlying.Spot, chain.Expiry.TTEYears)

	// 6. Skew Anomalies
	// We find the skew snapshot matching this expiry
	var skewSnap *market.IVSkewSnapshot
	for _, s := range surface.Skews {
		if s.Expiry == out.Expiry {
			skewSnap = &s
			break
		}
	}
	if skewSnap != nil {
		out.SkewAnomalies = computeSkewAnomalies(skewSnap)
	}

	// 7. Signals
	out.Signals = generateSignals(out, chain)

	// 8. Human Explain
	out.Explanation = generateExplanation(out)

	return out
}

// Helpers

type strikeData struct {
	K       float64
	CallOI  int64
	PutOI   int64
	CallVol int64
	PutVol  int64
}

func filterEligible(chain *market.ChainSnapshot) []strikeData {
	var res []strikeData

	// Iterate through chain strikes to ensure order and consistent keys
	for _, kFloat := range chain.ChainState.Strikes {
		kStr := fmt.Sprintf("%.0f", kFloat) // Format consistent with Chain Processor
		sc := chain.ByStrike[kStr]
		if sc == nil {
			continue
		}

		cOI, pOI := int64(0), int64(0)
		cVol, pVol := int64(0), int64(0)

		if sc.Call != nil {
			cOI = sc.Call.OpenInterest
			cVol = sc.Call.Volume
		}
		if sc.Put != nil {
			pOI = sc.Put.OpenInterest
			pVol = sc.Put.Volume
		}

		// Simple filter: Include if there is ANY Open Interest
		if cOI > 0 || pOI > 0 {
			res = append(res, strikeData{
				K:       kFloat,
				CallOI:  cOI,
				PutOI:   pOI,
				CallVol: cVol,
				PutVol:  pVol,
			})
		}
	}

	// Sort (already sorted by keys, but redundancy is safe)
	sort.Slice(res, func(i, j int) bool { return res[i].K < res[j].K })
	return res
}

func filterNearATM(chain *market.ChainSnapshot, all []strikeData) []strikeData {
	spot := chain.Underlying.Spot
	// Define window: +/- 3% log moneyness
	var res []strikeData
	limit := 0.03
	for _, s := range all {
		lm := math.Log(s.K / spot)
		if math.Abs(lm) <= limit {
			res = append(res, s)
		}
	}
	return res
}

func computePCR(all, near []strikeData) market.PCRMetrics {
	pcr := market.PCRMetrics{IsValid: true}

	// Total
	cOI, pOI := int64(0), int64(0)
	cVol, pVol := int64(0), int64(0)
	for _, s := range all {
		cOI += s.CallOI
		pOI += s.PutOI
		cVol += s.CallVol
		pVol += s.PutVol
	}

	if cOI > 100 { // Minimum threshold
		pcr.OIPCRTotal = float64(pOI) / float64(cOI)
	} else {
		pcr.IsValid = false
		pcr.Note = "Low Total Call OI"
	}

	if cVol > 100 {
		pcr.VolumePCR = float64(pVol) / float64(cVol)
	}

	// Near ATM
	cOIn, pOIn := int64(0), int64(0)
	for _, s := range near {
		cOIn += s.CallOI
		pOIn += s.PutOI
	}
	if cOIn > 50 {
		pcr.OIPCRNearATM = float64(pOIn) / float64(cOIn)
	}

	return pcr
}

func computeMaxPain(strikes []strikeData) market.MaxPainMetrics {
	if len(strikes) == 0 {
		return market.MaxPainMetrics{}
	}

	bestStrike := 0.0
	minPain := math.MaxFloat64

	// Evaluate pain at every strike K set
	for _, candidate := range strikes {
		pain := 0.0
		S := candidate.K

		for _, holder := range strikes {
			// Call Holder Value: MAX(S - K, 0) * OI
			if holder.CallOI > 0 {
				val := math.Max(S-holder.K, 0)
				pain += val * float64(holder.CallOI)
			}
			// Put Holder Value: MAX(K - S, 0) * OI
			if holder.PutOI > 0 {
				val := math.Max(holder.K-S, 0)
				pain += val * float64(holder.PutOI)
			}
		}

		if pain < minPain {
			minPain = pain
			bestStrike = S
		}
	}

	// Simple confidence: based on count
	conf := 0.0
	if len(strikes) > 10 {
		conf = 0.8
	} else {
		conf = 0.4
	}

	return market.MaxPainMetrics{
		Strike:     bestStrike,
		Confidence: conf,
	}
}

func computeOIWalls(strikes []strikeData, spot float64) market.OIWalls {
	w := market.OIWalls{
		TopCalls: []market.StrikeOI{},
		TopPuts:  []market.StrikeOI{},
	}

	// Find max Call OI above spot? Or absolute max?
	// Usually Walls are absolute max OI.
	// But meaningful walls are OTM.
	// "Call Wall: strike above spot with max call OI"
	// "Put Wall: strike below spot with max put OI"

	maxCallOI := int64(-1)
	maxPutOI := int64(-1)

	// Helpers for sorting
	var calls []market.StrikeOI
	var puts []market.StrikeOI

	for _, s := range strikes {
		calls = append(calls, market.StrikeOI{Strike: s.K, OI: s.CallOI})
		puts = append(puts, market.StrikeOI{Strike: s.K, OI: s.PutOI})

		// Call Wall (OTM preferred? Let's stick to definition: max OI usually acts as magnet/wall regardless, but OTM is resistance)
		// Definition from Prompt: "strike above spot with max call OI"
		if s.K >= spot {
			if s.CallOI > maxCallOI {
				maxCallOI = s.CallOI
				w.CallWallStrike = s.K
				w.CallWallOI = s.CallOI
			}
		}

		// Put Wall
		if s.K <= spot {
			if s.PutOI > maxPutOI {
				maxPutOI = s.PutOI
				w.PutWallStrike = s.K
				w.PutWallOI = s.PutOI
			}
		}
	}

	// Sort top 3
	sort.Slice(calls, func(i, j int) bool { return calls[i].OI > calls[j].OI })
	sort.Slice(puts, func(i, j int) bool { return puts[i].OI > puts[j].OI })

	if len(calls) > 3 {
		calls = calls[:3]
	}
	if len(puts) > 3 {
		puts = puts[:3]
	}

	w.TopCalls = calls
	w.TopPuts = puts

	return w
}

func computePinRisk(strikes []strikeData, spot float64, tte float64) market.PinRiskMetrics {
	// Only relevant if near expiry
	// e.g. TTE < 5 days (5/365 = 0.013)
	// Let's say TTE < 0.02

	pm := market.PinRiskMetrics{}

	if tte > 0.02 {
		return pm // Not near expiry enough
	}

	bestK := 0.0
	maxScore := -1.0

	for _, s := range strikes {
		totalOI := float64(s.CallOI + s.PutOI)
		dist := math.Abs(math.Log(s.K / spot))

		// Simple Score: TotalOI * exp(-decay * dist)
		// decay factor alpha.
		// If dist is 1% (0.01), we want high score.
		// If dist is 10% (0.10), low score.
		// alpha = 100 seems reasonable? exp(-1) at 1%.

		score := totalOI * math.Exp(-100.0*dist)

		if score > maxScore {
			maxScore = score
			bestK = s.K
		}
	}

	// Normalize Score?
	// Make it 0-1? Hard without global context.
	// Let's just return raw score relative to "Huge OI"
	// For "IsPinRisk", check if bestK is close to spot (dist < 0.01) AND score is high?

	isClose := math.Abs(math.Log(bestK/spot)) < 0.01

	pm.PinStrike = bestK
	pm.Score = maxScore // Raw score
	pm.IsPinRisk = isClose

	return pm
}

func computeSkewAnomalies(skew *market.IVSkewSnapshot) market.SkewAnomalyMetrics {
	sam := market.SkewAnomalyMetrics{Flags: []string{}}

	if skew == nil {
		return sam
	}

	// Heuristics
	// 1. Steep Slope
	if skew.Metrics.SkewSlope < -0.4 { // Arbitrary threshold for "Steep"
		sam.Flags = append(sam.Flags, "STEEP_SKEW")
		sam.IsAnomaly = true // Maybe
	}

	// 2. Curvature (Smile)
	if skew.Metrics.Curvature > 2.0 {
		sam.Flags = append(sam.Flags, "HIGH_CURVATURE")
	}

	return sam
}

func generateSignals(intel *market.ChainIntelSnapshot, chain *market.ChainSnapshot) []market.Signal {
	var sigs []market.Signal
	spot := chain.Underlying.Spot

	// Signal: Pin Risk
	if intel.PinRisk.IsPinRisk {
		sigs = append(sigs, market.Signal{
			Name:        "PIN_RISK_ZONE",
			Score:       0.9,
			Explanation: fmt.Sprintf("High OI Concentration near Spot at %.0f", intel.PinRisk.PinStrike),
		})
	}

	// Signal: Walls
	if spot > intel.OIWalls.PutWallStrike && spot < intel.OIWalls.CallWallStrike {
		// Inside walls
		rangePct := (intel.OIWalls.CallWallStrike - intel.OIWalls.PutWallStrike) / spot
		score := 0.5
		if rangePct < 0.05 {
			score = 0.8
		} // Tight range

		sigs = append(sigs, market.Signal{
			Name:        "BOUNDED_BY_WALLS",
			Score:       score,
			Explanation: fmt.Sprintf("Spot %.0f inside Put Wall %.0f and Call Wall %.0f", spot, intel.OIWalls.PutWallStrike, intel.OIWalls.CallWallStrike),
		})
	}

	// Signal: Max Pain Pull
	dist := (intel.MaxPain.Strike - spot) / spot
	if math.Abs(dist) < 0.02 && chain.Expiry.TTEYears < 0.02 {
		sigs = append(sigs, market.Signal{
			Name:        "MAX_PAIN_MAGNET",
			Score:       0.7,
			Explanation: fmt.Sprintf("Spot %.0f is close to Max Pain %.0f near expiry", spot, intel.MaxPain.Strike),
		})
	}

	return sigs
}

func generateExplanation(intel *market.ChainIntelSnapshot) []string {
	var lines []string

	lines = append(lines, fmt.Sprintf("PCR (OI Total): %.2f", intel.PCR.OIPCRTotal))
	lines = append(lines, fmt.Sprintf("Max Pain: %.0f (Conf: %.2f)", intel.MaxPain.Strike, intel.MaxPain.Confidence))
	lines = append(lines, fmt.Sprintf("Walls: Put %.0f | Call %.0f", intel.OIWalls.PutWallStrike, intel.OIWalls.CallWallStrike))

	if intel.PinRisk.IsPinRisk {
		lines = append(lines, fmt.Sprintf("WARN: Potential Pin Risk at %.0f", intel.PinRisk.PinStrike))
	}

	return lines
}
