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
	settings     Settings
	ivHistory    IVHistoryProvider
	priceHistory PriceHistoryProvider
}

func NewDetector(settings Settings, ivHist IVHistoryProvider, priceHist PriceHistoryProvider) *Detector {
	return &Detector{
		settings:     settings,
		ivHistory:    ivHist,
		priceHistory: priceHist,
	}
}

func (d *Detector) Detect(surface market.IVSurfaceSnapshot) market.RegimeState {
	state := market.RegimeState{
		AsOf:       surface.AsOf,
		Underlying: surface.Underlying,
	}

	// 1. Calculate IV30
	iv30, conf, method, err := InterpolateIV30(surface.Skews)
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
		state.IVReference.Method = method
	}

	state.IVReference.TenorDays = 30
	state.IVReference.IV = iv30
	state.IVReference.Confidence = conf

	// 2. Fetch Historical IV Data
	histLookback := 200 // Default lookback
	ivHistory, err := d.ivHistory.GetIV30History(surface.Underlying, surface.AsOf, histLookback)

	histMin := 0.0
	histMax := 0.0
	ivRank := 0.5
	ivPercentile := 0.5

	if err != nil || len(ivHistory) == 0 {
		state.Quality.Warnings = append(state.Quality.Warnings, "IV_HISTORY_MISSING")
		state.Decision.Rationale = append(state.Decision.Rationale, "WARN: No IV History")
		// Fallback defaults already set
	} else {
		// Compute Stats
		minV, maxV := ivHistory[0], ivHistory[0]
		for _, v := range ivHistory {
			if v < minV {
				minV = v
			}
			if v > maxV {
				maxV = v
			}
		}
		histMin = minV
		histMax = maxV

		ivRank = ComputeRankFromSeries(iv30, ivHistory)
		ivPercentile = ComputePercentileFromSeries(iv30, ivHistory)
	}

	state.HistoricalContext = market.HistoricalContext{
		LookbackDays: histLookback,
		MinIV:        histMin,
		MaxIV:        histMax,
		IVRank:       ivRank,
		IVPercentile: ivPercentile,
	}

	// 3. Realized Vol (HV20)
	hvWindow := 20
	// We need lookback + 1 for N returns, but fetching a bit more is safe
	closes, err := d.priceHistory.GetDailyCloses(surface.Underlying, surface.AsOf, hvWindow+5)

	hv20 := 0.0
	if err != nil || len(closes) < 2 {
		state.Quality.Warnings = append(state.Quality.Warnings, "HV_UNAVAILABLE")
		// Leave hv20 as 0
	} else {
		// Use last hvWindow + 1 closes to get hvWindow returns?
		// Actually ComputeHV takes slice of closes. N closes -> N-1 returns.
		// If we want 20 day HV, we need 21 closes.
		val, err := ComputeHV(closes, 252.0)
		if err != nil {
			state.Quality.Warnings = append(state.Quality.Warnings, "HV_CALC_FAIL")
		} else {
			hv20 = val
		}
	}

	ivHvRatio := 0.0
	ivHvSpread := 0.0
	if hv20 > 0 {
		ivHvRatio = iv30 / hv20
		ivHvSpread = iv30 - hv20
	}

	state.RealizedVol = market.RealizedVol{
		WindowDays: hvWindow,
		HV:         hv20,
		IVHVRatio:  ivHvRatio,
		IVHVSpread: ivHvSpread,
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
	if len(surface.Skews) > 0 {
		skewSource := surface.Skews[0] // Default
		state.Skew = market.RegimeSkew{
			ExpiryUsed: skewSource.Expiry,
			SkewSlope:  skewSource.Metrics.SkewSlope,
			Curvature:  skewSource.Metrics.Curvature,
		}
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
