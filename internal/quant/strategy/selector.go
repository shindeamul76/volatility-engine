package strategy

import (
	"fmt"
	"math"
	"sort"

	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/quant/pricing"
)

type Selector struct {
	// Configuration
}

func NewSelector() *Selector {
	return &Selector{}
}

// SelectStrategies generates ranked candidates based on inputs.
func (s *Selector) SelectStrategies(
	reg *market.RegimeState,
	surface *market.IVSurfaceSnapshot,
	intelSnap *market.ChainIntelSnapshot,
	chain *market.ChainSnapshot,
	fwd *market.ForwardState,
) ([]market.StrategyCandidate, error) {

	candidates := []market.StrategyCandidate{}

	// 1. Initial Guards
	if reg == nil || surface == nil || chain == nil {
		return candidates, fmt.Errorf("missing critical inputs")
	}
	if chain.LiquiditySummary.TradableNearATM == false {
		return candidates, fmt.Errorf("bad liquidity near ATM")
	}

	// 2. Expiry Selection logic (Regime based)
	// Prototype: We verify if the passed 'chain' matches our desired expiry window.
	// Since inputs provide a Single Chain, we essentially check if we SHOULD trade this chain.
	// User guide: "Choose based on regime... 7-21 DTE for short, 14-45 for long"
	tte := chain.Expiry.TTEYears
	dte := tte * 365.0

	isEvent := false // TODO: Check term structure slope from surface?
	// If near expiry IV is much higher than far, meaningful backwardation -> event?
	if surface.TermStructure.Metrics.TermSlope < -0.5 { // Arbitrary steep backwardation
		isEvent = true
	}

	// 3. Strategy Generation based on Regime
	// HIGH_VOL -> Sell Premium (Iron Condor, Iron Fly)
	// LOW_VOL -> Buy Premium (Straddle, Strangle)
	// TRANSITION -> Mix or Defined Risk

	rType := reg.Decision.Regime

	if rType == market.RegimeHighVol || rType == market.RegimeTransition {
		// Try Iron Condor
		if dte >= 2 && dte <= 45 { // Broad window for prototype
			cands := s.genIronCondor(chain, fwd, intelSnap, reg)
			candidates = append(candidates, cands...)
		}

		// Try Iron Fly (if Very High Vol or Pin Risk)
		// For prototype, if score > 0.7 or specific flag
		if reg.Decision.Score > 0.7 || intelSnap.PinRisk.IsPinRisk {
			cands := s.genIronFly(chain, fwd, intelSnap, reg)
			candidates = append(candidates, cands...)
		}
	}

	if rType == market.RegimeLowVol || isEvent {
		// Try Straddle / Strangle
		if dte >= 7 { // Avoid super near expiry for long vol unless gamma scalp (advanced)
			cands := s.genLongStraddle(chain, fwd, intelSnap, reg)
			candidates = append(candidates, cands...)

			cands2 := s.genLongStrangle(chain, fwd, intelSnap, reg)
			candidates = append(candidates, cands2...)
		}
	}

	// 4. Scoring & Ranking
	for i := range candidates {
		s.scoreCandidate(&candidates[i], reg, intelSnap)
	}

	// Sort by Score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Quality.Score > candidates[j].Quality.Score
	})

	// Return top 4
	if len(candidates) > 4 {
		return candidates[:4], nil
	}

	return candidates, nil
}

// Generators

func (s *Selector) genIronCondor(chain *market.ChainSnapshot, fwd *market.ForwardState, intelSnap *market.ChainIntelSnapshot, reg *market.RegimeState) []market.StrategyCandidate {
	// Template: Short Iron Condor
	// Goal: Sell OTM Put/Call, Buy wings.
	// Target Delta: .15 to .20 (Shorts)

	f := fwd.Forward.Mid
	if f == 0 {
		f = chain.Underlying.Spot
	} // Fallback

	strikes := s.enrichStrikesWithDelta(chain, f, fwd.Rates.RiskFreeRateCC)

	// Select Short Strikes
	shortPutK := s.findStrikeByDelta(strikes, -0.20, "PUT")
	shortCallK := s.findStrikeByDelta(strikes, 0.20, "CALL")

	// Adjust for Walls/Pin (Intel)
	// If ShortPut is inside a Put Wall, maybe move it?
	// Prototype: keeping simple delta selection first.

	if shortPutK == 0 || shortCallK == 0 {
		return nil
	}

	// Select Wings (Width)
	// Simple: 5% width or fixed steps?
	// Let's use 2-3 strikes away?
	// Let's try to find approx 1-2% OTM from short
	width := f * 0.015 // 1.5% width
	longPutK := s.findStrikeClosestTo(strikes, shortPutK-width)
	longCallK := s.findStrikeClosestTo(strikes, shortCallK+width)

	// Construct Legs
	legs := []market.StrategyLeg{
		s.buildLeg(chain, shortPutK, market.Put, "SELL", 1),
		s.buildLeg(chain, longPutK, market.Put, "BUY", 1),
		s.buildLeg(chain, shortCallK, market.Call, "SELL", 1),
		s.buildLeg(chain, longCallK, market.Call, "BUY", 1),
	}

	// Validate (check nil legs or confidence)
	if !s.validateLegs(legs) {
		return nil
	}

	// Layout
	cand := market.StrategyCandidate{
		ID:           fmt.Sprintf("ic_%s_%s", chain.Underlying.Symbol, chain.Expiry.Expiry.Format("20060102")),
		StrategyType: market.IronCondor,
		Intent:       "SHORT_VOL_DEFINED_RISK",
		Legs:         legs,
		Expiry:       chain.Expiry.Expiry.Format("2006-01-02"),
		AsOf:         chain.AsOf,
		Underlying:   chain.Underlying.Symbol,
		Rationale: market.Rationale{
			Regime:         reg.Decision.Regime,
			SelectionBasis: fmt.Sprintf("Regime is '%s' and DTE (%d) is within ideal 2-45 day window for premium selling.", reg.Decision.Regime, int(chain.Expiry.DaysToExpiry)),
			Thesis:         "Mindset: Volatility is overrated. We expect the market to stay range-bound or at least not move beyond the wings. We collect premium upfront and profit from time decay (Theta) and volatility crush (Vega).",
			LegSelection:   "Short strikes chosen at approx 20 Delta (High Probability OTM). Wings bought ~1.5% further out to define risk and cap margin requirement.",
			Reasons:        []string{"High Vol Regime", "High Probability of Profit (POP)", "Defined Risk"},
		},
	}

	s.calcMetrics(&cand)
	return []market.StrategyCandidate{cand}
}

func (s *Selector) genIronFly(chain *market.ChainSnapshot, fwd *market.ForwardState, intelSnap *market.ChainIntelSnapshot, reg *market.RegimeState) []market.StrategyCandidate {
	f := fwd.Forward.Mid
	if f == 0 {
		f = chain.Underlying.Spot
	}

	strikes := s.enrichStrikesWithDelta(chain, f, fwd.Rates.RiskFreeRateCC)

	// ATM
	atmK := s.findStrikeClosestTo(strikes, f)

	// Wings (Wider than condor usually, or same?)
	width := f * 0.02 // 2% width
	lowWing := s.findStrikeClosestTo(strikes, atmK-width)
	highWing := s.findStrikeClosestTo(strikes, atmK+width)

	legs := []market.StrategyLeg{
		s.buildLeg(chain, atmK, market.Call, "SELL", 1),
		s.buildLeg(chain, atmK, market.Put, "SELL", 1),
		s.buildLeg(chain, highWing, market.Call, "BUY", 1),
		s.buildLeg(chain, lowWing, market.Put, "BUY", 1),
	}

	if !s.validateLegs(legs) {
		return nil
	}

	cand := market.StrategyCandidate{
		ID:           fmt.Sprintf("if_%s_%s", chain.Underlying.Symbol, chain.Expiry.Expiry.Format("20060102")),
		StrategyType: market.IronFly,
		Intent:       "SHORT_VOL_PIN_TARGET",
		Legs:         legs,
		Expiry:       chain.Expiry.Expiry.Format("2006-01-02"),
		AsOf:         chain.AsOf,
		Underlying:   chain.Underlying.Symbol,
		Rationale: market.Rationale{
			Regime:         reg.Decision.Regime,
			SelectionBasis: fmt.Sprintf("Regime is '%s' with specific Pin Risk signals or very high vol.", reg.Decision.Regime),
			Thesis:         "Mindset: Aggressive Mean Reversion. We believe the price is pinned or will revert to the ATM level. We sell the 'meat' of the curve (ATM) for maximum credit.",
			LegSelection:   fmt.Sprintf("Sold ATM Straddle at %.0f. Bought protected wings ~2%% out. This creates a 'tent' profit zone centered on current price.", atmK),
			Reasons:        []string{"Aggressive Short Vol", "PCR/OI signals suggest Pin Risk", "Max Theta Decay"},
		},
	}
	s.calcMetrics(&cand)
	return []market.StrategyCandidate{cand}
}

func (s *Selector) genLongStraddle(chain *market.ChainSnapshot, fwd *market.ForwardState, intelSnap *market.ChainIntelSnapshot, reg *market.RegimeState) []market.StrategyCandidate {
	f := fwd.Forward.Mid
	if f == 0 {
		f = chain.Underlying.Spot
	}
	strikes := s.enrichStrikesWithDelta(chain, f, fwd.Rates.RiskFreeRateCC)

	atmK := s.findStrikeClosestTo(strikes, f)

	legs := []market.StrategyLeg{
		s.buildLeg(chain, atmK, market.Call, "BUY", 1),
		s.buildLeg(chain, atmK, market.Put, "BUY", 1),
	}
	if !s.validateLegs(legs) {
		return nil
	}

	cand := market.StrategyCandidate{
		ID:           fmt.Sprintf("straddle_%s_%s", chain.Underlying.Symbol, chain.Expiry.Expiry.Format("20060102")),
		StrategyType: market.LongStraddle,
		Intent:       "LONG_VOL_DIRECTION_NEUTRAL",
		Legs:         legs,
		Expiry:       chain.Expiry.Expiry.Format("2006-01-02"),
		AsOf:         chain.AsOf,
		Underlying:   chain.Underlying.Symbol,
		Rationale: market.Rationale{
			Regime:         reg.Decision.Regime,
			SelectionBasis: fmt.Sprintf("Regime is '%s' (or Event predicted). Expecting explosive move.", reg.Decision.Regime),
			Thesis:         "Mindset: Volatility is cheap. We expect a large move in EITHER direction that exceeds the breakeven (Premium Paid). We are Long Gamma and Long Vega.",
			LegSelection:   fmt.Sprintf("Bought ATM Call and Put at %.0f. This offers the highest Gamma (sensitivity to move) but also highest Theta decay.", atmK),
			Reasons:        []string{"Low Vol (Cheap Entry)", "Expect Vol Expansion/Event", "Direction Neutral"},
		},
	}
	s.calcMetrics(&cand)
	return []market.StrategyCandidate{cand}
}

func (s *Selector) genLongStrangle(chain *market.ChainSnapshot, fwd *market.ForwardState, intelSnap *market.ChainIntelSnapshot, reg *market.RegimeState) []market.StrategyCandidate {
	f := fwd.Forward.Mid
	if f == 0 {
		f = chain.Underlying.Spot
	}
	strikes := s.enrichStrikesWithDelta(chain, f, fwd.Rates.RiskFreeRateCC)

	// Delta 25 Strangle
	callK := s.findStrikeByDelta(strikes, 0.25, "CALL")
	putK := s.findStrikeByDelta(strikes, -0.25, "PUT")

	legs := []market.StrategyLeg{
		s.buildLeg(chain, callK, market.Call, "BUY", 1),
		s.buildLeg(chain, putK, market.Put, "BUY", 1),
	}
	if !s.validateLegs(legs) {
		return nil
	}

	cand := market.StrategyCandidate{
		ID:           fmt.Sprintf("strangle_%s_%s", chain.Underlying.Symbol, chain.Expiry.Expiry.Format("20060102")),
		StrategyType: market.LongStrangle,
		Intent:       "LONG_VOL_CHEAPER",
		Legs:         legs,
		Expiry:       chain.Expiry.Expiry.Format("2006-01-02"),
		AsOf:         chain.AsOf,
		Underlying:   chain.Underlying.Symbol,
		Rationale: market.Rationale{
			Regime:         reg.Decision.Regime,
			SelectionBasis: "Low Volatility environment, seeking lower cost entry than Straddle.",
			Thesis:         "Mindset: We expect a significant move, but want to reduce upfront debit. We sacrifice some probability (need larger move) for better leverage/ROI if it hits.",
			LegSelection:   "Bought 25 Delta Call and Put (OTM). This reduces cost compared to ATM Straddle but requires a larger move to become profitable.",
			Reasons:        []string{"Low Vol", "Cheaper than Straddle", "High Leverage on breakout"},
		},
	}
	s.calcMetrics(&cand)
	return []market.StrategyCandidate{cand}
}

// Helpers & Calc

type strikeDelta struct {
	Strike    float64
	CallDelta float64
	PutDelta  float64
}

func (s *Selector) enrichStrikesWithDelta(chain *market.ChainSnapshot, forward float64, rate float64) []strikeDelta {
	var res []strikeDelta
	tte := chain.Expiry.TTEYears

	for _, kFloat := range chain.ChainState.Strikes {
		kStr := fmt.Sprintf("%.0f", kFloat)
		sc := chain.ByStrike[kStr]
		if sc == nil {
			continue
		}

		// Approximate IV if we don't have surface lookup handy
		// We could use IV from surface inputs if available, or just say fixed vol (prototype)
		// Or better: use the IV from `DownstreamReadySets` if mapped?
		// For prototype, I will use a simple fixed IV or extract from chain if I had it.
		// Wait, `pricing` package calculates Delta.
		// I need IV.
		// Accessing IV is tricky without `IVSurface` mapping back to strict strike.
		// Let's assume constant IV (ATM) for delta sorting for now, improving later.
		ivEst := 0.20 // Benchmark

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

func (s *Selector) buildLeg(chain *market.ChainSnapshot, strike float64, optType market.OptionType, side string, qty int) market.StrategyLeg {
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
		leg.Confidence = 1.0 // Placeholder
		if !opt.Quality.IsTradable {
			leg.Confidence = 0.0
		}
	}
	return leg
}

func (s *Selector) validateLegs(legs []market.StrategyLeg) bool {
	for _, l := range legs {
		if l.Mark <= 0 || l.Confidence < 0.1 {
			return false
		}
	}
	return true
}

func (s *Selector) calcMetrics(c *market.StrategyCandidate) {
	premium := 0.0
	for _, l := range c.Legs {
		amount := l.Mark * float64(l.Qty)
		if l.Side == "BUY" {
			premium += amount
		} else {
			premium -= amount // Credit is negative net premium? Or use accounting convention.
		}
	}

	// Convention: NetPremium positive = Debit, Negative = Credit?
	// User prompt: "Credit = (Sell) - (Buy)".
	// My loop: +Buy -Sell = Net Debit.
	// If result is negative, it's a credit.

	if premium < 0 {
		c.Entry.PremiumType = "CREDIT"
		c.Entry.NetPremium = -premium
		c.Metrics.MaxProfit = -premium
		// Max Loss for Iron Condor?
		// Width - Credit approximately.
		// Need logic to know widths.
		// Placeholder.
		c.Metrics.MaxLossApprox = 500.0 // Todo
	} else {
		c.Entry.PremiumType = "DEBIT"
		c.Entry.NetPremium = premium
		c.Metrics.MaxLossApprox = premium
		c.Metrics.MaxProfit = -1 // Infinite usually
	}
}

func (s *Selector) scoreCandidate(c *market.StrategyCandidate, reg *market.RegimeState, intel *market.ChainIntelSnapshot) {
	// Simple scoring
	score := 0.5

	// Alignment
	if c.StrategyType == market.IronCondor && reg.Decision.Regime == market.RegimeHighVol {
		score += 0.2
	}
	if c.StrategyType == market.LongStraddle && reg.Decision.Regime == market.RegimeLowVol {
		score += 0.2
	}

	// Intel Bonus
	if c.StrategyType == market.IronFly && intel.PinRisk.IsPinRisk {
		score += 0.2
	}

	c.Quality.Score = score
}
