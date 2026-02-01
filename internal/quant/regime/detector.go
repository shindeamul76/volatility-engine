package regime

import (
	"math"

	"volatility-engine/internal/domain/market"
)

type Settings struct {
	HighVolThreshold float64 // e.g., 1.15 Ratio or 0.70 Rank/Pct
	LowVolThreshold  float64 // e.g., 1.00 Ratio or 0.30 Rank/Pct

	// Scoring Weights
	WeightPercentile float64
	WeightRank       float64
	WeightRichness   float64
}

func DefaultSettings() Settings {
	return Settings{
		HighVolThreshold: 1.15,
		LowVolThreshold:  1.00,
		WeightPercentile: 0.50,
		WeightRank:       0.30,
		WeightRichness:   0.20,
	}
}

type Detector struct {
	settings Settings
}

func NewDetector(settings Settings) *Detector {
	return &Detector{settings: settings}
}

func (d *Detector) Detect(surface market.IVSurfaceSnapshot) market.RegimeState {
	state := market.RegimeState{
		AsOf:       surface.AsOf,
		Underlying: surface.Underlying,
	}

	// 1. Calculate IV30
	iv30, conf, err := InterpolateIV30(surface.Skews)
	if err != nil {
		state.Decision.Rationale = append(state.Decision.Rationale, "IV30 Calc Failed: "+err.Error())
		state.Quality.Warnings = append(state.Quality.Warnings, "IV30_FAIL")
		// Fallback to nearest ATM?
		if len(surface.Skews) > 0 {
			iv30 = surface.Skews[0].Metrics.ATMVol
			conf = surface.Skews[0].Metrics.Confidence
			state.IVReference.Method = "NEAREST_FALLBACK"
		}
	} else {
		state.IVReference.Method = "VARIANCE_INTERP"
	}

	state.IVReference.TenorDays = 30
	state.IVReference.IV = iv30
	state.IVReference.Confidence = conf

	// 2. Fetch/Mock Historical Data
	// User requested "constant values" for prototype
	// Mocking N=200 days. Min=0.11, Max=0.28, HV=0.14
	histMin := 0.11
	histMax := 0.28
	hv20 := 0.14
	histLookback := 200

	// Simulated History slice for Percentile (mocking distribution)
	// We'll just infer percentile from rank roughly or hardcode for now
	// User Example: Rank=0.33, Percentile=0.40.
	// Let's implement a dynamic mock that is consistent with the current IV30
	// If IV30 matches user example (0.1667), we should return 0.40 percentile.
	// Let's create a synthetic history array: [Min...Max] uniform
	histData := make([]float64, histLookback)
	step := (histMax - histMin) / float64(histLookback-1)
	for i := 0; i < histLookback; i++ {
		histData[i] = histMin + float64(i)*step
	}

	state.HistoricalContext = market.HistoricalContext{
		LookbackDays: histLookback,
		MinIV:        histMin,
		MaxIV:        histMax,
		IVRank:       ComputeRank(iv30, histMin, histMax),
		IVPercentile: ComputePercentile(iv30, histData),
	}

	// 3. Realized Vol
	state.RealizedVol = market.RealizedVol{
		WindowDays: 20,
		HV:         hv20,
		IVHVRatio:  iv30 / hv20,
		IVHVSpread: iv30 - hv20,
	}
	if hv20 <= 0 {
		state.Quality.Warnings = append(state.Quality.Warnings, "HV_UNAVAILABLE")
		// Adjust calc if needed
	}

	// 4. Term Structure & Skew Context
	if len(surface.Skews) >= 2 {
		near := surface.Skews[0]
		next := surface.Skews[1]
		state.TermStructure = market.RegimeTermStructure{
			NearExpiry:  near.Expiry,
			NextExpiry:  next.Expiry,
			NearATMIV:   near.Metrics.ATMVol,
			NextATMIV:   next.Metrics.ATMVol,
			TermPremium: near.Metrics.ATMVol - next.Metrics.ATMVol,
		}
	}

	// Skew from used expiry
	// Finding skew for expiry closest to 30d or just nearest?
	// User: "from near expiry or IV30"
	// Let's pick Nearest >= 30d if possible, or just first valid.
	skewSource := surface.Skews[0] // Default
	state.Skew = market.RegimeSkew{
		ExpiryUsed: skewSource.Expiry,
		SkewSlope:  skewSource.Metrics.SkewSlope,
		Curvature:  skewSource.Metrics.Curvature,
	}

	// 5. Compute Score
	// Score = 0.5*p + 0.3*r + 0.2*clamp(0.5*(x-1))

	p := state.HistoricalContext.IVPercentile
	r := state.HistoricalContext.IVRank
	x := state.RealizedVol.IVHVRatio

	richness := 0.0
	if x > 1.0 {
		richness = 0.5 * (x - 1.0) // 1.2 -> 0.1, 1.4 -> 0.2
	}
	richness = math.Max(0.0, math.Min(1.0, richness))

	score := d.settings.WeightPercentile*p +
		d.settings.WeightRank*r +
		d.settings.WeightRichness*richness

	state.Decision.Score = score

	// 6. Classification
	isHighVol := (p >= 0.70 || r >= 0.70) && (x >= d.settings.HighVolThreshold)
	isLowVol := (p <= 0.30 || r <= 0.30) && (x <= d.settings.LowVolThreshold)

	if isHighVol {
		state.Decision.Regime = market.RegimeHighVol
		state.Decision.Bias = market.BiasSellPremium
	} else if isLowVol {
		state.Decision.Regime = market.RegimeLowVol
		state.Decision.Bias = market.BiasBuyPremium
	} else {
		state.Decision.Regime = market.RegimeTransition
		state.Decision.Bias = market.BiasNeutral
	}

	// Rationale
	if p >= 0.70 {
		state.Decision.Rationale = append(state.Decision.Rationale, "High Historical Percentile")
	}
	if x >= 1.20 {
		state.Decision.Rationale = append(state.Decision.Rationale, "Rich IV vs HV")
	}
	if state.TermStructure.TermPremium > 0.02 {
		state.Decision.Rationale = append(state.Decision.Rationale, "Event Risk (Term Premium)")
	}

	state.Quality.Confidence = conf // Inherit base confidence
	// Penalize if low
	if conf < 0.5 {
		state.Quality.Warnings = append(state.Quality.Warnings, "LOW_CONFIDENCE_INPUT")
	}

	return state
}
