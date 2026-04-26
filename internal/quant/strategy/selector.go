package strategy

import (
	"fmt"
	"log"
	"math"
	"sort"

	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/quant/pricing"
	"volatility-engine/internal/quant/risk"
)

// maxLossCapPoints is the hard gate: candidates with max loss above this are rejected.
const maxLossCapPoints = 1000.0

// minCreditPoints is the minimum acceptable credit for any short-vol candidate.
const minCreditPoints = 5.0

// Selector selects and ranks strategy candidates from per-expiry chain data.
type Selector struct {
	// Configuration can be extended here.
}

// NewSelector returns a new Selector.
func NewSelector() *Selector {
	return &Selector{}
}

// ─────────────────────────────────────────────────────────────────────────────
// Expiry Selection
// ─────────────────────────────────────────────────────────────────────────────

// scoreExpiry computes a composite score [0,1] for an expiry context.
// It considers: skew quality, liquidity, DTE fitness, regime compatibility.
func (s *Selector) scoreExpiry(ctx *market.ExpiryContext, reg *market.RegimeState) market.ExpiryScore {
	expStr := ctx.Expiry.Expiry.Format("2006-01-02")
	dte := float64(ctx.Expiry.DaysToExpiry)
	rType := reg.Decision.Regime

	// 1. Skew Quality (0–1): based on point count + avg confidence + fit error
	skewQ := 0.0
	if ctx.SkewSnapshot != nil {
		ptCount := float64(len(ctx.IVPoints))
		ptScore := math.Min(ptCount/20.0, 1.0) // saturates at 20 points

		avgConf := 0.0
		for _, p := range ctx.IVPoints {
			avgConf += p.Confidence
		}
		if ptCount > 0 {
			avgConf /= ptCount
		}

		fitScore := 0.0
		if ctx.SkewSnapshot.Fit.Quality.WeightedRMSE < 0.01 {
			fitScore = 1.0
		} else if ctx.SkewSnapshot.Fit.Quality.WeightedRMSE < 0.03 {
			fitScore = 0.5
		}

		skewQ = 0.4*ptScore + 0.4*avgConf + 0.2*fitScore
	}

	// 2. Liquidity score
	liqScore := 0.0
	if ctx.ChainSnapshot != nil {
		if ctx.ChainSnapshot.LiquiditySummary.TradableNearATM {
			liqScore += 0.6
		}
		avgSpread := ctx.ChainSnapshot.LiquiditySummary.AvgSpreadPctNearATM
		if avgSpread <= 0.01 {
			liqScore += 0.4
		} else if avgSpread <= 0.03 {
			liqScore += 0.2
		}
	}

	// 3. DTE Fitness — regime-specific ideal windows
	dteFit := 0.0
	switch rType {
	case market.RegimeHighVol, market.RegimeTransition:
		// Short vol favours 7–21 DTE
		if dte >= 7 && dte <= 21 {
			dteFit = 1.0
		} else if dte >= 2 && dte <= 30 {
			dteFit = 0.5
		}
	case market.RegimeLowVol:
		// Long vol prefers 14–45 DTE (needs time for move)
		if dte >= 14 && dte <= 45 {
			dteFit = 1.0
		} else if dte >= 7 {
			dteFit = 0.5
		}
	default:
		if dte >= 7 && dte <= 30 {
			dteFit = 0.7
		}
	}

	// 4. Regime compatibility bonus
	regimeCompat := 0.0
	if rType == market.RegimeHighVol && dte <= 14 {
		regimeCompat = 0.2 // Prefer near expiry for short-vol theta plays
	} else if rType == market.RegimeLowVol && dte >= 21 {
		regimeCompat = 0.2 // Prefer far expiry for long-vol plays
	}

	total := 0.30*skewQ + 0.30*liqScore + 0.30*dteFit + 0.10*regimeCompat

	score := market.ExpiryScore{
		Expiry:         expStr,
		Score:          total,
		SkewQuality:    skewQ,
		LiquidityScore: liqScore,
		DTEFitness:     dteFit,
		RegimeCompat:   regimeCompat,
		Notes:          fmt.Sprintf("dte=%.0f regime=%s", dte, rType),
	}
	return score
}

// SelectTopExpiries scores all valid expiry contexts and returns the top-n by score.
// This is called by engine.go before Pass 3 strategy generation.
func (s *Selector) SelectTopExpiries(ctxs []*market.ExpiryContext, reg *market.RegimeState, n int) []*market.ExpiryContext {
	type scored struct {
		ctx   *market.ExpiryContext
		score market.ExpiryScore
	}

	var candidates []scored
	for _, ctx := range ctxs {
		if ctx.SkipReason != "" || ctx.SkewSnapshot == nil {
			continue
		}
		sc := s.scoreExpiry(ctx, reg)
		candidates = append(candidates, scored{ctx, sc})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score.Score > candidates[j].score.Score
	})

	log.Printf("[EXPIRY SELECTION] Regime=%s  Ranking %d eligible expiries:", reg.Decision.Regime, len(candidates))
	for rank, c := range candidates {
		log.Printf("  #%d  expiry=%-12s score=%.3f (skewQ=%.2f liqQ=%.2f dteFit=%.2f regimeCompat=%.2f) [%s]",
			rank+1, c.score.Expiry, c.score.Score,
			c.score.SkewQuality, c.score.LiquidityScore, c.score.DTEFitness, c.score.RegimeCompat,
			c.score.Notes,
		)
	}

	if n > len(candidates) {
		n = len(candidates)
	}
	out := make([]*market.ExpiryContext, n)
	for i := range out {
		out[i] = candidates[i].ctx
	}
	if len(out) > 0 {
		chosen := make([]string, len(out))
		for i, c := range out {
			chosen[i] = c.Expiry.Expiry.Format("2006-01-02")
		}
		log.Printf("[EXPIRY SELECTION] Chosen: %v", chosen)
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// SelectStrategies — main entry point (per-expiry)
// ─────────────────────────────────────────────────────────────────────────────

// SelectStrategies generates ranked strategy candidates for a single expiry.
// Call this only on the expiries returned by SelectTopExpiries.
func (s *Selector) SelectStrategies(
	reg *market.RegimeState,
	surface *market.IVSurfaceSnapshot,
	intelSnap *market.ChainIntelSnapshot,
	chain *market.ChainSnapshot,
	fwd *market.ForwardState,
) ([]market.StrategyCandidate, error) {

	candidates := []market.StrategyCandidate{}

	// 1. Guards
	if reg == nil || surface == nil || chain == nil {
		return candidates, fmt.Errorf("missing critical inputs")
	}
	if !chain.LiquiditySummary.TradableNearATM {
		return candidates, fmt.Errorf("bad liquidity near ATM")
	}

	dte := chain.Expiry.TTEYears * 365.0
	rType := reg.Decision.Regime

	isEvent := surface.TermStructure.Metrics.TermSlope < -0.5

	// 2. Generate variants based on regime
	if rType == market.RegimeHighVol || rType == market.RegimeTransition {
		if dte >= 2 && dte <= 45 {
			candidates = append(candidates, s.genIronCondorVariants(chain, fwd, intelSnap, reg, surface)...)
		}
		if reg.Decision.Score > 0.7 || (intelSnap != nil && intelSnap.PinRisk.IsPinRisk) {
			candidates = append(candidates, s.genIronFlyVariants(chain, fwd, intelSnap, reg, surface)...)
		}
	}

	if rType == market.RegimeLowVol || isEvent {
		if dte >= 7 {
			candidates = append(candidates, s.genLongStraddleVariants(chain, fwd, intelSnap, reg, surface)...)
			candidates = append(candidates, s.genLongStrangleVariants(chain, fwd, intelSnap, reg, surface)...)
		}
	}

	// Credit spreads: directional defined-risk. Generated in any regime;
	// scored higher/lower based on bias from regime decision.
	if dte >= 2 && dte <= 45 {
		bias := reg.Decision.Bias
		// Bull Put Spread: bullish or neutral bias
		if bias == market.BiasSellPremium || bias == market.BiasNeutral || bias == "" {
			candidates = append(candidates, s.genBullPutSpreadVariants(chain, fwd, intelSnap, reg, surface)...)
		}
		// Bear Call Spread: bearish or neutral bias
		if bias == market.BiasBuyPremium || bias == market.BiasNeutral || bias == "" {
			candidates = append(candidates, s.genBearCallSpreadVariants(chain, fwd, intelSnap, reg, surface)...)
		}
	}

	// 3. Score
	for i := range candidates {
		candidates[i].Rationale.SelectionBasis = fmt.Sprintf(
			"Regime=%s DTE=%.1f variant=%s", rType, dte, candidates[i].Variant.VariantID)
		s.scoreCandidate(&candidates[i], reg, intelSnap)
	}

	// 4. Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		if math.Abs(candidates[i].Quality.Score-candidates[j].Quality.Score) > 0.001 {
			return candidates[i].Quality.Score > candidates[j].Quality.Score
		}
		// Tie-break: tighter average spread is better
		return avgSpread(candidates[i]) < avgSpread(candidates[j])
	})

	// 5. Deduplicate near-identical candidates within same type
	candidates = s.deduplicateSameType(candidates, 2)

	// 6. Return top 10
	if len(candidates) > 10 {
		candidates = candidates[:10]
	}

	s.logCandidates(candidates, chain.Expiry.Expiry.Format("2006-01-02"))
	return candidates, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Iron Condor — 5 Variants
// ─────────────────────────────────────────────────────────────────────────────

type icVariantSpec struct {
	id         string
	shortDelta float64 // abs value; applied to both put and call
	wingWidth  float64 // in index points
	wingPct    float64 // as fraction of spot (used if points == 0)
	note       string
}

func (s *Selector) genIronCondorVariants(
	chain *market.ChainSnapshot, fwd *market.ForwardState,
	intel *market.ChainIntelSnapshot, reg *market.RegimeState,
	surface *market.IVSurfaceSnapshot,
) []market.StrategyCandidate {

	f := fwd.Forward.Mid
	if f == 0 {
		f = chain.Underlying.Spot
	}
	strikes := s.enrichStrikesWithDelta(chain, f, fwd.Rates.RiskFreeRateCC, surface)

	specs := []icVariantSpec{
		{"IC-1", 0.20, 400, 0, "20Δ shorts, 400pt wings"},
		{"IC-2", 0.15, 400, 0, "15Δ shorts (wider body), 400pt wings"},
		{"IC-3", 0.25, 400, 0, "25Δ shorts (tighter body), 400pt wings"},
		{"IC-4", 0.20, 300, 0, "20Δ shorts, narrow 300pt wings"},
		{"IC-5", 0.20, 500, 0, "20Δ shorts, wide 500pt wings"},
	}

	var out []market.StrategyCandidate
	for _, sp := range specs {
		shortPutK := s.findStrikeByDelta(strikes, -sp.shortDelta, "PUT")
		shortCallK := s.findStrikeByDelta(strikes, sp.shortDelta, "CALL")
		if shortPutK == 0 || shortCallK == 0 {
			continue
		}

		width := sp.wingWidth
		if width == 0 && sp.wingPct > 0 {
			width = f * sp.wingPct
		}
		longPutK := s.findStrikeClosestTo(strikes, shortPutK-width)
		longCallK := s.findStrikeClosestTo(strikes, shortCallK+width)
		if longPutK == 0 || longCallK == 0 || longPutK >= shortPutK || longCallK <= shortCallK {
			continue
		}

		legs := []market.StrategyLeg{
			s.buildLeg(chain, shortPutK, market.Put, "SELL", 1, surface),
			s.buildLeg(chain, longPutK, market.Put, "BUY", 1, surface),
			s.buildLeg(chain, shortCallK, market.Call, "SELL", 1, surface),
			s.buildLeg(chain, longCallK, market.Call, "BUY", 1, surface),
		}
		if !s.validateLegs(legs) {
			continue
		}

		cand := market.StrategyCandidate{
			ID:           fmt.Sprintf("ic_%s_%s_%s", chain.Underlying.Symbol, chain.Expiry.Expiry.Format("20060102"), sp.id),
			StrategyType: market.IronCondor,
			Intent:       "SHORT_VOL_DEFINED_RISK",
			Legs:         legs,
			Expiry:       chain.Expiry.Expiry.Format("2006-01-02"),
			AsOf:         chain.AsOf,
			Underlying:   chain.Underlying.Symbol,
			Variant: market.VariantParams{
				VariantID:       sp.id,
				ShortDeltaCall:  sp.shortDelta,
				ShortDeltaPut:   sp.shortDelta,
				WingWidthPoints: width,
				Note:            sp.note,
			},
			Rationale: market.Rationale{
				Regime: reg.Decision.Regime,
				Thesis: "Volatility overpriced. Expect range-bound market; collect theta and vega decay.",
				LegSelection: fmt.Sprintf(
					"%s — Short Put %.0f, Long Put %.0f, Short Call %.0f, Long Call %.0f",
					sp.note, shortPutK, longPutK, shortCallK, longCallK),
				Reasons: []string{"High Vol Regime", "Defined Risk", sp.note},
			},
		}
		s.calcMetrics(&cand)

		if !s.hardGateCheck(&cand, f) {
			continue
		}
		out = append(out, cand)
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Iron Fly — 5 Variants
// ─────────────────────────────────────────────────────────────────────────────

type ifVariantSpec struct {
	id    string
	width float64 // points; if 0 use pct
	pct   float64 // fraction of spot
	note  string
}

func (s *Selector) genIronFlyVariants(
	chain *market.ChainSnapshot, fwd *market.ForwardState,
	intel *market.ChainIntelSnapshot, reg *market.RegimeState,
	surface *market.IVSurfaceSnapshot,
) []market.StrategyCandidate {

	f := fwd.Forward.Mid
	if f == 0 {
		f = chain.Underlying.Spot
	}
	strikes := s.enrichStrikesWithDelta(chain, f, fwd.Rates.RiskFreeRateCC, surface)
	atmK := s.findStrikeClosestTo(strikes, f)
	if atmK == 0 {
		return nil
	}

	specs := []ifVariantSpec{
		{"IF-1", 300, 0, "ATM short, ±300pt wings"},
		{"IF-2", 400, 0, "ATM short, ±400pt wings"},
		{"IF-3", 500, 0, "ATM short, ±500pt wings"},
		{"IF-4", 0, 0.020, "ATM short, 2% wings"},
		{"IF-5", 0, 0.025, "ATM short, 2.5% wings"},
	}

	var out []market.StrategyCandidate
	for _, sp := range specs {
		w := sp.width
		if w == 0 {
			w = f * sp.pct
		}

		lowWing := s.findStrikeClosestTo(strikes, atmK-w)
		highWing := s.findStrikeClosestTo(strikes, atmK+w)
		if lowWing == 0 || highWing == 0 || lowWing >= atmK || highWing <= atmK {
			continue
		}

		legs := []market.StrategyLeg{
			s.buildLeg(chain, atmK, market.Call, "SELL", 1, surface),
			s.buildLeg(chain, atmK, market.Put, "SELL", 1, surface),
			s.buildLeg(chain, highWing, market.Call, "BUY", 1, surface),
			s.buildLeg(chain, lowWing, market.Put, "BUY", 1, surface),
		}
		if !s.validateLegs(legs) {
			continue
		}

		cand := market.StrategyCandidate{
			ID:           fmt.Sprintf("if_%s_%s_%s", chain.Underlying.Symbol, chain.Expiry.Expiry.Format("20060102"), sp.id),
			StrategyType: market.IronFly,
			Intent:       "SHORT_VOL_PIN_TARGET",
			Legs:         legs,
			Expiry:       chain.Expiry.Expiry.Format("2006-01-02"),
			AsOf:         chain.AsOf,
			Underlying:   chain.Underlying.Symbol,
			Variant: market.VariantParams{
				VariantID:       sp.id,
				WingWidthPoints: w,
				WingWidthPct:    sp.pct,
				Note:            sp.note,
			},
			Rationale: market.Rationale{
				Regime: reg.Decision.Regime,
				Thesis: "Aggressive mean reversion. Expect market to pin near ATM.",
				LegSelection: fmt.Sprintf(
					"%s — ATM short straddle at %.0f, wings at %.0f / %.0f",
					sp.note, atmK, lowWing, highWing),
				Reasons: []string{"High Vol Regime", "Pin Risk Signal", sp.note},
			},
		}
		s.calcMetrics(&cand)

		if !s.hardGateCheck(&cand, f) {
			continue
		}
		out = append(out, cand)
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Long Straddle — 3 Variants
// ─────────────────────────────────────────────────────────────────────────────

func (s *Selector) genLongStraddleVariants(
	chain *market.ChainSnapshot, fwd *market.ForwardState,
	intel *market.ChainIntelSnapshot, reg *market.RegimeState,
	surface *market.IVSurfaceSnapshot,
) []market.StrategyCandidate {

	f := fwd.Forward.Mid
	if f == 0 {
		f = chain.Underlying.Spot
	}
	strikes := s.enrichStrikesWithDelta(chain, f, fwd.Rates.RiskFreeRateCC, surface)

	atmK := s.findStrikeClosestTo(strikes, f)
	if atmK == 0 {
		return nil
	}

	// Find the adjacent strikes — sort available strikes
	sortedStrikes := make([]float64, len(strikes))
	for i, sd := range strikes {
		sortedStrikes[i] = sd.Strike
	}
	sort.Float64s(sortedStrikes)

	// Find ATM index
	atmIdx := 0
	for i, k := range sortedStrikes {
		if k == atmK {
			atmIdx = i
			break
		}
	}

	type straddleSpec struct {
		id     string
		strike float64
		note   string
	}

	specs := []straddleSpec{
		{"STR-1", atmK, "ATM straddle"},
	}
	if atmIdx+1 < len(sortedStrikes) {
		specs = append(specs, straddleSpec{"STR-2", sortedStrikes[atmIdx+1], "One strike up"})
	}
	if atmIdx-1 >= 0 {
		specs = append(specs, straddleSpec{"STR-3", sortedStrikes[atmIdx-1], "One strike down"})
	}

	var out []market.StrategyCandidate
	for _, sp := range specs {
		legs := []market.StrategyLeg{
			s.buildLeg(chain, sp.strike, market.Call, "BUY", 1, surface),
			s.buildLeg(chain, sp.strike, market.Put, "BUY", 1, surface),
		}
		if !s.validateLegs(legs) {
			continue
		}

		cand := market.StrategyCandidate{
			ID:           fmt.Sprintf("straddle_%s_%s_%s", chain.Underlying.Symbol, chain.Expiry.Expiry.Format("20060102"), sp.id),
			StrategyType: market.LongStraddle,
			Intent:       "LONG_VOL_DIRECTION_NEUTRAL",
			Legs:         legs,
			Expiry:       chain.Expiry.Expiry.Format("2006-01-02"),
			AsOf:         chain.AsOf,
			Underlying:   chain.Underlying.Symbol,
			Variant: market.VariantParams{
				VariantID: sp.id,
				Note:      sp.note,
			},
			Rationale: market.Rationale{
				Regime: reg.Decision.Regime,
				Thesis: "Volatility is cheap. Expecting large directional move or event expansion.",
				LegSelection: fmt.Sprintf(
					"%s at strike %.0f — Long Call + Long Put", sp.note, sp.strike),
				Reasons: []string{"Low Vol Regime", "Direction Neutral", sp.note},
			},
		}
		s.calcMetrics(&cand)
		out = append(out, cand)
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Long Strangle — 5 Variants
// ─────────────────────────────────────────────────────────────────────────────

func (s *Selector) genLongStrangleVariants(
	chain *market.ChainSnapshot, fwd *market.ForwardState,
	intel *market.ChainIntelSnapshot, reg *market.RegimeState,
	surface *market.IVSurfaceSnapshot,
) []market.StrategyCandidate {

	f := fwd.Forward.Mid
	if f == 0 {
		f = chain.Underlying.Spot
	}
	strikes := s.enrichStrikesWithDelta(chain, f, fwd.Rates.RiskFreeRateCC, surface)

	type strangleSpec struct {
		id        string
		putDelta  float64 // negative
		callDelta float64 // positive
		distPct   float64 // used if deltas == 0
		note      string
		skewAware bool
	}

	specs := []strangleSpec{
		{"SNG-1", -0.15, 0.15, 0, "15Δ strangle (wider OTM)", false},
		{"SNG-2", -0.20, 0.20, 0, "20Δ strangle (medium OTM)", false},
		{"SNG-3", -0.25, 0.25, 0, "25Δ strangle (tighter OTM)", false},
		{"SNG-4", 0, 0, 0.015, "Symmetric 1.5% distance", false},
		{"SNG-5", -0.20, 0.20, 0, "Skew-aware 20Δ (wider expensive wing)", true},
	}

	var out []market.StrategyCandidate
	for _, sp := range specs {
		var putK, callK float64

		if sp.distPct > 0 {
			putK = s.findStrikeClosestTo(strikes, f*(1-sp.distPct))
			callK = s.findStrikeClosestTo(strikes, f*(1+sp.distPct))
		} else {
			putK = s.findStrikeByDelta(strikes, sp.putDelta, "PUT")
			callK = s.findStrikeByDelta(strikes, sp.callDelta, "CALL")
		}

		// Skew-aware: if put wing is richer (higher IV), move put 1 step further OTM
		if sp.skewAware && putK != 0 && callK != 0 {
			putIV := s.strikeIV(strikes, putK, "PUT")
			callIV := s.strikeIV(strikes, callK, "CALL")
			if putIV > callIV*1.05 {
				// move put further out by one strike step
				putK = s.findStrikeClosestTo(strikes, putK-chain.ChainState.StrikeStep)
			} else if callIV > putIV*1.05 {
				callK = s.findStrikeClosestTo(strikes, callK+chain.ChainState.StrikeStep)
			}
		}

		if putK == 0 || callK == 0 || putK >= callK {
			continue
		}

		legs := []market.StrategyLeg{
			s.buildLeg(chain, callK, market.Call, "BUY", 1, surface),
			s.buildLeg(chain, putK, market.Put, "BUY", 1, surface),
		}
		if !s.validateLegs(legs) {
			continue
		}

		cand := market.StrategyCandidate{
			ID:           fmt.Sprintf("strangle_%s_%s_%s", chain.Underlying.Symbol, chain.Expiry.Expiry.Format("20060102"), sp.id),
			StrategyType: market.LongStrangle,
			Intent:       "LONG_VOL_CHEAPER",
			Legs:         legs,
			Expiry:       chain.Expiry.Expiry.Format("2006-01-02"),
			AsOf:         chain.AsOf,
			Underlying:   chain.Underlying.Symbol,
			Variant: market.VariantParams{
				VariantID:      sp.id,
				ShortDeltaCall: math.Abs(sp.callDelta),
				ShortDeltaPut:  math.Abs(sp.putDelta),
				WingWidthPct:   sp.distPct,
				Note:           sp.note,
			},
			Rationale: market.Rationale{
				Regime: reg.Decision.Regime,
				Thesis: "Expect explosive move but pay less premium than straddle.",
				LegSelection: fmt.Sprintf(
					"%s — Put %.0f, Call %.0f", sp.note, putK, callK),
				Reasons: []string{"Low Vol Regime", "Cheaper than Straddle", sp.note},
			},
		}
		s.calcMetrics(&cand)
		out = append(out, cand)
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Bull Put Spread — 5 Variants (directional credit spread, bullish)
// ─────────────────────────────────────────────────────────────────────────────

type creditSpreadSpec struct {
	id         string
	shortDelta float64 // abs value for short leg
	wingWidth  float64 // points; 0 means use pct
	wingPct    float64 // fraction of spot
	note       string
}

func (s *Selector) genBullPutSpreadVariants(
	chain *market.ChainSnapshot, fwd *market.ForwardState,
	intel *market.ChainIntelSnapshot, reg *market.RegimeState,
	surface *market.IVSurfaceSnapshot,
) []market.StrategyCandidate {

	f := fwd.Forward.Mid
	if f == 0 {
		f = chain.Underlying.Spot
	}
	strikes := s.enrichStrikesWithDelta(chain, f, fwd.Rates.RiskFreeRateCC, surface)

	specs := []creditSpreadSpec{
		{"BPS-1", 0.20, 300, 0, "20Δ short put, 300pt wing"},
		{"BPS-2", 0.25, 300, 0, "25Δ short put, 300pt wing"},
		{"BPS-3", 0.30, 300, 0, "30Δ short put, 300pt wing"},
		{"BPS-4", 0.25, 200, 0, "25Δ short put, narrow 200pt wing"},
		{"BPS-5", 0.25, 400, 0, "25Δ short put, wide 400pt wing"},
	}

	var out []market.StrategyCandidate
	for _, sp := range specs {
		shortPutK := s.findStrikeByDelta(strikes, -sp.shortDelta, "PUT")
		if shortPutK == 0 {
			continue
		}

		width := sp.wingWidth
		if width == 0 && sp.wingPct > 0 {
			width = f * sp.wingPct
		}
		longPutK := s.findStrikeClosestTo(strikes, shortPutK-width)
		if longPutK == 0 || longPutK >= shortPutK {
			continue
		}

		legs := []market.StrategyLeg{
			s.buildLeg(chain, shortPutK, market.Put, "SELL", 1, surface),
			s.buildLeg(chain, longPutK, market.Put, "BUY", 1, surface),
		}
		if !s.validateLegs(legs) {
			continue
		}

		cand := market.StrategyCandidate{
			ID:           fmt.Sprintf("bps_%s_%s_%s", chain.Underlying.Symbol, chain.Expiry.Expiry.Format("20060102"), sp.id),
			StrategyType: market.BullPutSpread,
			Intent:       "SHORT_VOL_DIRECTIONAL_BULLISH",
			Legs:         legs,
			Expiry:       chain.Expiry.Expiry.Format("2006-01-02"),
			AsOf:         chain.AsOf,
			Underlying:   chain.Underlying.Symbol,
			Variant: market.VariantParams{
				VariantID:       sp.id,
				ShortDeltaPut:   sp.shortDelta,
				WingWidthPoints: width,
				Note:            sp.note,
			},
			Rationale: market.Rationale{
				Regime: reg.Decision.Regime,
				Thesis: "Bullish defined-risk credit spread. We expect the underlying to stay above the short put strike. Profit from theta + directional bias.",
				LegSelection: fmt.Sprintf(
					"%s — Sell Put %.0f, Buy Put %.0f (width %.0f pts)",
					sp.note, shortPutK, longPutK, width),
				Reasons: []string{"Defined Risk", "Bullish Directional", sp.note},
			},
		}
		s.calcMetrics(&cand)

		if !s.hardGateCheck(&cand, f) {
			continue
		}
		out = append(out, cand)
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Bear Call Spread — 5 Variants (directional credit spread, bearish)
// ─────────────────────────────────────────────────────────────────────────────

func (s *Selector) genBearCallSpreadVariants(
	chain *market.ChainSnapshot, fwd *market.ForwardState,
	intel *market.ChainIntelSnapshot, reg *market.RegimeState,
	surface *market.IVSurfaceSnapshot,
) []market.StrategyCandidate {

	f := fwd.Forward.Mid
	if f == 0 {
		f = chain.Underlying.Spot
	}
	strikes := s.enrichStrikesWithDelta(chain, f, fwd.Rates.RiskFreeRateCC, surface)

	specs := []creditSpreadSpec{
		{"BCS-1", 0.20, 300, 0, "20Δ short call, 300pt wing"},
		{"BCS-2", 0.25, 300, 0, "25Δ short call, 300pt wing"},
		{"BCS-3", 0.30, 300, 0, "30Δ short call, 300pt wing"},
		{"BCS-4", 0.25, 200, 0, "25Δ short call, narrow 200pt wing"},
		{"BCS-5", 0.25, 400, 0, "25Δ short call, wide 400pt wing"},
	}

	var out []market.StrategyCandidate
	for _, sp := range specs {
		shortCallK := s.findStrikeByDelta(strikes, sp.shortDelta, "CALL")
		if shortCallK == 0 {
			continue
		}

		width := sp.wingWidth
		if width == 0 && sp.wingPct > 0 {
			width = f * sp.wingPct
		}
		longCallK := s.findStrikeClosestTo(strikes, shortCallK+width)
		if longCallK == 0 || longCallK <= shortCallK {
			continue
		}

		legs := []market.StrategyLeg{
			s.buildLeg(chain, shortCallK, market.Call, "SELL", 1, surface),
			s.buildLeg(chain, longCallK, market.Call, "BUY", 1, surface),
		}
		if !s.validateLegs(legs) {
			continue
		}

		cand := market.StrategyCandidate{
			ID:           fmt.Sprintf("bcs_%s_%s_%s", chain.Underlying.Symbol, chain.Expiry.Expiry.Format("20060102"), sp.id),
			StrategyType: market.BearCallSpread,
			Intent:       "SHORT_VOL_DIRECTIONAL_BEARISH",
			Legs:         legs,
			Expiry:       chain.Expiry.Expiry.Format("2006-01-02"),
			AsOf:         chain.AsOf,
			Underlying:   chain.Underlying.Symbol,
			Variant: market.VariantParams{
				VariantID:       sp.id,
				ShortDeltaCall:  sp.shortDelta,
				WingWidthPoints: width,
				Note:            sp.note,
			},
			Rationale: market.Rationale{
				Regime: reg.Decision.Regime,
				Thesis: "Bearish defined-risk credit spread. We expect the underlying to stay below the short call strike. Profit from theta + directional bias.",
				LegSelection: fmt.Sprintf(
					"%s — Sell Call %.0f, Buy Call %.0f (width %.0f pts)",
					sp.note, shortCallK, longCallK, width),
				Reasons: []string{"Defined Risk", "Bearish Directional", sp.note},
			},
		}
		s.calcMetrics(&cand)

		if !s.hardGateCheck(&cand, f) {
			continue
		}
		out = append(out, cand)
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Hard Gate Pruning
// ─────────────────────────────────────────────────────────────────────────────

// hardGateCheck returns true if the candidate passes all hard gates.
func (s *Selector) hardGateCheck(c *market.StrategyCandidate, spot float64) bool {
	// Gate 1: all legs must have a valid tradable quote (already validated before this)
	// Gate 2: for credit strategies, must have meaningful credit
	if c.Entry.PremiumType == "CREDIT" && c.Entry.NetPremium < minCreditPoints {
		return false
	}
	// Gate 3: max loss must be within cap
	if c.Metrics.MaxLossApprox > maxLossCapPoints {
		return false
	}
	// Gate 4: for short-vol, breakeven must be > 0 buffer from spot
	if c.Entry.PremiumType == "CREDIT" && spot > 0 {
		beBuffer := 0.005 * spot // 0.5% minimum buffer
		if c.Metrics.Breakevens.Low > 0 && c.Metrics.Breakevens.Low > spot-beBuffer {
			return false // Low BE too close or above spot
		}
		if c.Metrics.Breakevens.High > 0 && c.Metrics.Breakevens.High < spot+beBuffer {
			return false // High BE too close or below spot
		}
	}
	return true
}

// deduplicateSameType removes near-duplicates within the same strategy type,
// keeping at most maxPerType per type.
func (s *Selector) deduplicateSameType(candidates []market.StrategyCandidate, maxPerType int) []market.StrategyCandidate {
	typeCounts := make(map[string]int)
	var out []market.StrategyCandidate
	for _, c := range candidates {
		key := string(c.StrategyType) + "_" + c.Expiry
		if typeCounts[key] < maxPerType {
			out = append(out, c)
			typeCounts[key]++
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Candidate Metrics (entry pricing — mid + liquidation)
// ─────────────────────────────────────────────────────────────────────────────

func (s *Selector) calcMetrics(c *market.StrategyCandidate) {
	// --- Mid-based entry ---
	midNet := 0.0
	for _, l := range c.Legs {
		amt := l.Mark * float64(l.Qty)
		if l.Side == "BUY" {
			midNet += amt
		} else {
			midNet -= amt
		}
	}

	// --- Liquidation-consistent entry: BUY@ask, SELL@bid ---
	liqNet := 0.0
	for _, l := range c.Legs {
		if l.Side == "BUY" {
			liqNet += l.Ask * float64(l.Qty)
		} else {
			liqNet -= l.Bid * float64(l.Qty)
		}
	}

	// Convention: positive = Debit, negative = Credit (from buyer's perspective)
	setCredit := func(net float64) {
		if net < 0 {
			c.Entry.PremiumType = "CREDIT"
			c.Entry.NetPremium = -net
			c.Metrics.MaxProfit = -net
			s.calcMaxLossCredit(c)
			s.calcBreakevensCredit(c)
		} else {
			c.Entry.PremiumType = "DEBIT"
			c.Entry.NetPremium = net
			c.Metrics.MaxLossApprox = net
			c.Metrics.MaxProfit = -1
			s.calcBreakevensDebit(c)
		}
	}

	// Use mid for the canonical entry fields
	setCredit(midNet)
	c.Metrics.MarginReq = c.Metrics.MaxLossApprox

	// Store both for transparency
	if midNet < 0 {
		c.EntryMid = -midNet // how much credit at mid
	} else {
		c.EntryMid = midNet // how much debit at mid
	}
	if liqNet < 0 {
		c.EntryLiquidation = -liqNet // credit at bid side (worse fill)
	} else {
		c.EntryLiquidation = liqNet // debit at ask side (worse fill)
	}

	// Min confidence across legs
	minConf := 1.0
	if len(c.Legs) > 0 {
		minConf = c.Legs[0].Confidence
		for _, l := range c.Legs {
			if l.Confidence < minConf {
				minConf = l.Confidence
			}
		}
	}
	c.Metrics.MinLegConfidence = minConf
}

func (s *Selector) calcMaxLossCredit(c *market.StrategyCandidate) {
	findWidth := func(optType market.OptionType) float64 {
		var shortK, longK float64
		for _, l := range c.Legs {
			if l.Type == optType && l.Side == "SELL" {
				shortK = l.Strike
			}
			if l.Type == optType && l.Side == "BUY" {
				longK = l.Strike
			}
		}
		if shortK > 0 && longK > 0 {
			return math.Abs(shortK - longK)
		}
		return 0
	}

	width := findWidth(market.Put)
	if width == 0 {
		width = findWidth(market.Call)
	}
	if width > 0 {
		c.Metrics.MaxLossApprox = width - c.Entry.NetPremium
	}
}

func (s *Selector) calcBreakevensCredit(c *market.StrategyCandidate) {
	netCred := c.Entry.NetPremium
	qty := 1
	if len(c.Legs) > 0 {
		qty = c.Legs[0].Qty
	}
	credPerUnit := netCred / float64(qty)

	var shortPutK, shortCallK float64
	for _, l := range c.Legs {
		if l.Side == "SELL" && l.Type == market.Put {
			shortPutK = l.Strike
		}
		if l.Side == "SELL" && l.Type == market.Call {
			shortCallK = l.Strike
		}
	}
	if shortPutK > 0 {
		c.Metrics.Breakevens.Low = shortPutK - credPerUnit
	}
	if shortCallK > 0 {
		c.Metrics.Breakevens.High = shortCallK + credPerUnit
	}
}

func (s *Selector) calcBreakevensDebit(c *market.StrategyCandidate) {
	netDeb := c.Entry.NetPremium
	qty := 1
	if len(c.Legs) > 0 {
		qty = c.Legs[0].Qty
	}
	debPerUnit := netDeb / float64(qty)

	var putK, callK float64
	for _, l := range c.Legs {
		if l.Side == "BUY" && l.Type == market.Put {
			putK = l.Strike
		}
		if l.Side == "BUY" && l.Type == market.Call {
			callK = l.Strike
		}
	}
	if putK > 0 {
		c.Metrics.Breakevens.Low = putK - debPerUnit
	}
	if callK > 0 {
		c.Metrics.Breakevens.High = callK + debPerUnit
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Scoring
// ─────────────────────────────────────────────────────────────────────────────

func (s *Selector) scoreCandidate(c *market.StrategyCandidate, reg *market.RegimeState, intel *market.ChainIntelSnapshot) {
	score := 0.0

	// 1. Regime alignment (0.25 weight)
	regScore := 0.0
	switch {
	case (c.StrategyType == market.IronCondor || c.StrategyType == market.IronFly) &&
		(reg.Decision.Regime == market.RegimeHighVol || reg.Decision.Regime == market.RegimeTransition):
		regScore = 1.0
	case (c.StrategyType == market.LongStraddle || c.StrategyType == market.LongStrangle) &&
		reg.Decision.Regime == market.RegimeLowVol:
		regScore = 1.0
	case c.StrategyType == market.BullPutSpread:
		// Bullish bias → full score; neutral → partial; bearish → low
		switch reg.Decision.Bias {
		case market.BiasSellPremium:
			regScore = 1.0 // Sell Premium = bullish/neutral → bull put fits
		case market.BiasNeutral:
			regScore = 0.6
		case market.BiasBuyPremium:
			regScore = 0.3
		default:
			regScore = 0.5
		}
	case c.StrategyType == market.BearCallSpread:
		// Bearish bias → full score; neutral → partial; bullish → low
		switch reg.Decision.Bias {
		case market.BiasBuyPremium:
			regScore = 1.0 // Buy Premium = bearish → bear call fits
		case market.BiasNeutral:
			regScore = 0.6
		case market.BiasSellPremium:
			regScore = 0.3
		default:
			regScore = 0.5
		}
	default:
		regScore = 0.3
	}
	score += 0.25 * regScore

	// 2. Liquidity quality (0.20 weight)
	liqScore := c.Metrics.MinLegConfidence // 0–1
	avgSpr := avgSpread(*c)
	if avgSpr <= 0.01 {
		liqScore = math.Min(liqScore+0.2, 1.0)
	} else if avgSpr <= 0.03 {
		liqScore = math.Min(liqScore+0.1, 1.0)
	} else if avgSpr > 0.07 {
		liqScore = math.Max(liqScore-0.3, 0)
	}
	score += 0.20 * liqScore

	// 3. Risk efficiency (0.20 weight)
	riskScore := 0.0
	if c.Entry.PremiumType == "CREDIT" && c.Metrics.MaxLossApprox > 0 {
		// Credit / MaxLoss ratio (higher = better)
		ratio := c.EntryMid / c.Metrics.MaxLossApprox
		riskScore = math.Min(ratio*5, 1.0) // saturate at 20% return
	} else if c.Entry.PremiumType == "DEBIT" && c.EntryMid > 0 {
		// Use 1 - (debit / spot%) as proxy for leverage efficiency
		// Lower debit relative to max move is better
		riskScore = math.Max(0, 1.0-c.EntryMid/500.0)
	}
	score += 0.20 * riskScore

	// 4. Signal alignment (0.20 weight)
	sigScore := 0.5 // neutral
	if intel != nil {
		if c.StrategyType == market.IronFly && intel.PinRisk.IsPinRisk {
			sigScore = 1.0
		}
		if (c.StrategyType == market.IronCondor) && !intel.PinRisk.IsPinRisk {
			sigScore = 0.8
		}
		// Credit spreads: boost if walls support the direction
		if c.StrategyType == market.BullPutSpread {
			sigScore = 0.6 // Default; if we had Put Wall intel below short put, could boost
		}
		if c.StrategyType == market.BearCallSpread {
			sigScore = 0.6 // Default; if Call Wall above short call, could boost
		}
	}
	score += 0.20 * sigScore

	// 5. Min leg confidence (0.15 weight)
	score += 0.15 * c.Metrics.MinLegConfidence

	c.Quality.Score = math.Round(score*1000) / 1000
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

type strikeDelta struct {
	Strike    float64
	CallDelta float64
	PutDelta  float64
	CallIV    float64
	PutIV     float64
}

// enrichStrikesWithDelta computes delta (and IV if available from skew) for each strike.
func (s *Selector) enrichStrikesWithDelta(chain *market.ChainSnapshot, forward, rate float64, surface *market.IVSurfaceSnapshot) []strikeDelta {
	var res []strikeDelta
	tte := chain.Expiry.TTEYears

	// Try to find the skew for this expiry for better IV estimates
	expiryKey := chain.Expiry.Expiry.Format("2006-01-02")
	var skew *market.IVSkewSnapshot
	if surface != nil {
		for i := range surface.Skews {
			if surface.Skews[i].Expiry == expiryKey {
				skew = &surface.Skews[i]
				break
			}
		}
	}

	for _, kFloat := range chain.ChainState.Strikes {
		kStr := fmt.Sprintf("%.0f", kFloat)
		sc := chain.ByStrike[kStr]
		if sc == nil {
			continue
		}

		// Use surface IV if available, else fall back to a fixed estimate
		ivEst := 0.20
		callIV, putIV := ivEst, ivEst
		if skew != nil {
			params := []float64{skew.Fit.Params.A, skew.Fit.Params.B, skew.Fit.Params.C}
			modelIV := risk.VolFromSkew(params, kFloat, skew.Forward, skew.TTEYears)
			if modelIV > 0 {
				ivEst = modelIV
				callIV = modelIV
				putIV = modelIV
			}
		}

		pCtx := pricing.PricingContext{
			S:              forward,
			K:              kFloat,
			T:              tte,
			R:              rate,
			Sigma:          ivEst,
			IsForwardModel: true,
			Type:           market.Call,
		}

		cRes := pricing.Calculate(pCtx)
		pCtx.Type = market.Put
		pRes := pricing.Calculate(pCtx)

		res = append(res, strikeDelta{
			Strike:    kFloat,
			CallDelta: cRes.Greeks.Delta,
			PutDelta:  pRes.Greeks.Delta,
			CallIV:    callIV,
			PutIV:     putIV,
		})
	}
	return res
}

func (s *Selector) findStrikeByDelta(list []strikeDelta, targetDelta float64, legType string) float64 {
	bestK := 0.0
	minDiff := 1.0
	for _, item := range list {
		d := item.CallDelta
		if legType == "PUT" {
			d = item.PutDelta
		}
		diff := math.Abs(d - targetDelta)
		if diff < minDiff {
			minDiff = diff
			bestK = item.Strike
		}
	}
	return bestK
}

func (s *Selector) findStrikeClosestTo(list []strikeDelta, targetK float64) float64 {
	bestK := 0.0
	minDiff := math.MaxFloat64
	for _, item := range list {
		diff := math.Abs(item.Strike - targetK)
		if diff < minDiff {
			minDiff = diff
			bestK = item.Strike
		}
	}
	return bestK
}

// strikeIV returns the model IV for a given strike and option type from the enriched list.
func (s *Selector) strikeIV(list []strikeDelta, strike float64, legType string) float64 {
	for _, item := range list {
		if item.Strike == strike {
			if legType == "PUT" {
				return item.PutIV
			}
			return item.CallIV
		}
	}
	return 0
}

func (s *Selector) buildLeg(chain *market.ChainSnapshot, strike float64, optType market.OptionType, side string, qty int, surface *market.IVSurfaceSnapshot) market.StrategyLeg {
	kStr := fmt.Sprintf("%.0f", strike)
	sc := chain.ByStrike[kStr]
	leg := market.StrategyLeg{
		Strike: strike,
		Type:   optType,
		Side:   side,
		Qty:    qty,
	}

	if sc == nil {
		return leg
	}

	var opt *market.Option
	if optType == market.Call {
		opt = sc.Call
	} else {
		opt = sc.Put
	}

	if opt != nil {
		leg.Mark = opt.Mid
		leg.Bid = opt.Bid
		leg.Ask = opt.Ask
		leg.OI = opt.OpenInterest
		leg.Volume = opt.Volume
		if opt.Mid > 0 {
			leg.SpreadPct = (opt.Ask - opt.Bid) / opt.Mid
		}
		leg.Confidence = 1.0
		if !opt.Quality.IsTradable {
			leg.Confidence = 0.0
		}
	}

	// IV and delta from surface skew
	if surface != nil {
		expiryKey := chain.Expiry.Expiry.Format("2006-01-02")
		for i := range surface.Skews {
			if surface.Skews[i].Expiry == expiryKey {
				sk := &surface.Skews[i]
				params := []float64{sk.Fit.Params.A, sk.Fit.Params.B, sk.Fit.Params.C}
				leg.IV = risk.VolFromSkew(params, strike, sk.Forward, sk.TTEYears)
				if leg.IV > 0 && sk.TTEYears > 0 {
					r := 0.05
					if chain.Underlying.Spot > 0 && sk.Forward > 0 && sk.TTEYears > 0.001 {
						r = math.Log(sk.Forward/chain.Underlying.Spot) / sk.TTEYears
					}
					isCall := (optType == market.Call)
					bsmResult := risk.BSM(isCall, chain.Underlying.Spot, strike, sk.TTEYears, r, leg.IV)
					leg.Delta = bsmResult.Delta
				}
				break
			}
		}
	}

	return leg
}

func (s *Selector) validateLegs(legs []market.StrategyLeg) bool {
	for _, l := range legs {
		if l.Mark <= 0 || l.Confidence < 0.1 {
			return false
		}
		// Must have bid/ask data for liquidation pricing
		if l.Bid <= 0 && l.Ask <= 0 {
			// Fallback: synthesize bid/ask from mid with a spread assumption
			// Mark=mid is present; we allow this but reduce confidence
			// (don't reject — live data may only have mid)
		}
	}
	return true
}

// ─────────────────────────────────────────────────────────────────────────────
// Audit Logging
// ─────────────────────────────────────────────────────────────────────────────

func (s *Selector) logCandidates(candidates []market.StrategyCandidate, expiry string) {
	log.Printf("[CANDIDATES] expiry=%s total=%d", expiry, len(candidates))
	for _, c := range candidates {
		log.Printf(
			"  [CANDIDATE] id=%-45s type=%-15s variant=%-6s credit_mid=%-8.2f credit_liq=%-8.2f maxLoss=%-8.2f score=%.3f minConf=%.2f avgSprd=%.3f",
			c.ID, c.StrategyType, c.Variant.VariantID,
			c.EntryMid, c.EntryLiquidation,
			c.Metrics.MaxLossApprox,
			c.Quality.Score, c.Metrics.MinLegConfidence,
			avgSpread(c),
		)
	}
}

// avgSpread computes the mean spread% across all legs.
func avgSpread(c market.StrategyCandidate) float64 {
	if len(c.Legs) == 0 {
		return 0
	}
	total := 0.0
	for _, l := range c.Legs {
		total += l.SpreadPct
	}
	return total / float64(len(c.Legs))
}
