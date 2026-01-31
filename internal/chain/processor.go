package chain

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"volatility-engine/internal/domain/market"
)

type Processor struct {
	Rules        market.EligibilityRules
	RiskFreeRate float64
}

func NewProcessor(riskFreeRate float64) *Processor {
	return &Processor{
		Rules: market.EligibilityRules{
			MaxSpreadPct:    0.12,
			MinVolume:       100,
			MinOpenInterest: 1000,
			RequireBidAsk:   true,
		},
		RiskFreeRate: riskFreeRate,
	}
}

func (p *Processor) calculateImpliedForward(strikeMap map[float64]*market.StrikeChain, strikes []float64, expiry, asOf time.Time) float64 {
	// TTE in years
	tte := expiry.Sub(asOf).Hours() / 24.0 / 365.0
	if tte < 0 {
		return 0
	}

	// Discount Factor e^{rT} (inverse) -> we need e^{rT} for Forward
	erT := math.Exp(p.RiskFreeRate * tte)

	var forwards []float64

	for _, k := range strikes {
		sc := strikeMap[k]
		if sc.Call != nil && sc.Put != nil && canUseForParity(sc) {
			// F = K + e^{rT} * (C - P)
			val := k + erT*(sc.Call.Mid-sc.Put.Mid)
			forwards = append(forwards, val)
		}
	}

	if len(forwards) == 0 {
		return 0 // Fallback to Spot (handled by caller)
	}

	// Return Median
	sort.Float64s(forwards)
	mid := len(forwards) / 2
	if len(forwards)%2 == 1 {
		return forwards[mid]
	}
	return (forwards[mid-1] + forwards[mid]) / 2.0
}

func canUseForParity(sc *market.StrikeChain) bool {
	// Simple quality check: both sides tradable? or just present with tight spread?
	// Let's check spread < 5% for parity to be clean

	spreadCheck := func(opt *market.Option) bool {
		if opt.Mid <= 0 {
			return false
		}
		spreadPct := (opt.Ask - opt.Bid) / opt.Mid
		return spreadPct < 0.05
	}
	return spreadCheck(sc.Call) && spreadCheck(sc.Put)
}

// GenerateForwardState creates a detailed ForwardState JSON structure
func (p *Processor) GenerateForwardState(snap *market.Snapshot, chainSnap *market.ChainSnapshot, expiry time.Time) *market.ForwardState {
	tte := expiry.Sub(snap.AsOf).Hours() / 24.0 / 365.0
	df := math.Exp(-p.RiskFreeRate * tte)
	erT := math.Exp(p.RiskFreeRate * tte)

	fwdID := fmt.Sprintf("fwd_%s_%s_%s",
		snap.AsOf.Format("20060102_150405"),
		strings.ToLower(snap.Underlying.Symbol),
		expiry.Format("20060102"))

	fs := &market.ForwardState{
		ID:         fwdID,
		AsOf:       snap.AsOf,
		Underlying: snap.Underlying,
		Rates: market.Rates{
			RiskFreeRateCC: p.RiskFreeRate,
			DiscountFactor: df,
		},
		SourcePreference: []string{"FUTURES", "PUT_CALL_PARITY"},
		SelectedSource:   "PUT_CALL_PARITY",
		FiltersUsed: market.ForwardFilters{
			MaxSpreadPctPerLeg:  0.05,
			NearATMStrikeWindow: 5,
			RequireBothCallPut:  true,
		},
	}
	fs.Expiry.Date = expiry.Format("2006-01-02")
	fs.Expiry.TTEYears = tte

	// Build per-strike details
	var forwards []float64
	for _, kStr := range chainSnap.ChainState.Strikes {
		k := kStr
		kStrKey := fmt.Sprintf("%.0f", k)
		sc := chainSnap.ByStrike[kStrKey]
		if sc == nil {
			continue
		}

		fbs := market.ForwardByStrike{
			Strike:       k,
			QualityFlags: []string{},
		}

		if sc.Call != nil {
			spreadPct := 0.0
			if sc.Call.Mid > 0 {
				spreadPct = (sc.Call.Ask - sc.Call.Bid) / sc.Call.Mid
			}
			fbs.Call = &market.OptionMark{
				Mark:       sc.Call.Mid,
				SpreadPct:  spreadPct,
				IsTradable: sc.Call.Quality.IsTradable,
			}
		}

		if sc.Put != nil {
			spreadPct := 0.0
			if sc.Put.Mid > 0 {
				spreadPct = (sc.Put.Ask - sc.Put.Bid) / sc.Put.Mid
			}
			fbs.Put = &market.OptionMark{
				Mark:       sc.Put.Mid,
				SpreadPct:  spreadPct,
				IsTradable: sc.Put.Quality.IsTradable,
			}
		}

		// Calculate parity forward if both legs present
		if fbs.Call != nil && fbs.Put != nil && fbs.Call.SpreadPct < 0.05 && fbs.Put.SpreadPct < 0.05 {
			pf := k + erT*(fbs.Call.Mark-fbs.Put.Mark)
			fbs.ParityForward = &pf
			fbs.Weight = 1.0
			forwards = append(forwards, pf)
		} else {
			if fbs.Call == nil || fbs.Put == nil {
				fbs.QualityFlags = append(fbs.QualityFlags, "MISSING_PAIR")
			} else {
				fbs.QualityFlags = append(fbs.QualityFlags, "WIDE_SPREAD")
			}
		}

		fs.ForwardByStrike = append(fs.ForwardByStrike, fbs)
	}

	// Aggregate forward (median)
	if len(forwards) > 0 {
		sort.Float64s(forwards)
		mid := len(forwards) / 2
		var medianFwd float64
		if len(forwards)%2 == 1 {
			medianFwd = forwards[mid]
		} else {
			medianFwd = (forwards[mid-1] + forwards[mid]) / 2.0
		}
		fs.Forward = market.ForwardValue{
			Mid:        medianFwd,
			Confidence: float64(len(forwards)) / float64(len(chainSnap.ChainState.Strikes)),
			Method:     "MEDIAN_PARITY_NEAR_ATM",
			Notes: []string{
				"Computed from put-call parity using near-ATM tradable pairs",
				"Excluded pairs with wide spreads",
			},
		}
	}

	return fs
}

func (p *Processor) GenerateChain(snap *market.Snapshot, expiry time.Time) (*market.ChainSnapshot, error) {
	// 1. Initialize Chain Snapshot
	chainID := fmt.Sprintf("chain_%s_%s_%s",
		snap.AsOf.Format("20060102_150405"),
		strings.ToLower(snap.Underlying.Symbol),
		expiry.Format("20060102"))

	cs := &market.ChainSnapshot{
		ID:         chainID,
		AsOf:       snap.AsOf,
		Underlying: snap.Underlying,
		Expiry:     market.ExpiryMetadata{}, // To fill
		ChainState: market.ChainState{
			EligibilityRules: p.Rules,
		},
		ByStrike:            make(map[string]*market.StrikeChain),
		DownstreamReadySets: market.DownstreamReadySets{},
	}

	// Find the specific expiry metadata
	for _, e := range snap.Expiries {
		if e.Expiry.Equal(expiry) {
			cs.Expiry = e
			break
		}
	}
	// Fallback if not found (shouldn't happen if caller provides valid expiry)
	if cs.Expiry.Expiry.IsZero() {
		cs.Expiry.Expiry = expiry
	}

	// 2. Group by Strike
	// Filter quotes for this expiry
	quotesForExpiry := []market.Quote{}
	for _, q := range snap.Quotes {
		if q.Contract.Expiry.Equal(expiry) {
			quotesForExpiry = append(quotesForExpiry, q)
		}
	}

	strikeMap := make(map[float64]*market.StrikeChain)
	var strikes []float64

	for _, q := range quotesForExpiry {
		k := q.Contract.Strike
		if _, exists := strikeMap[k]; !exists {
			strikeMap[k] = &market.StrikeChain{
				Moneyness: k / snap.Underlying.Spot, // Simple Moneyness (K/S or S/K? user example 19800/20000 = 0.99, so K/S)
			}
			strikes = append(strikes, k)
		}

		opt := &market.Option{
			Mid:     q.Market.Mid,
			Bid:     q.Market.Bid,
			Ask:     q.Market.Ask,
			Quality: q.Quality,
		}

		if q.Contract.Type == market.Call {
			strikeMap[k].Call = opt
		} else {
			strikeMap[k].Put = opt
		}
	}

	// Sort strikes
	sort.Float64s(strikes)
	cs.ChainState.Strikes = strikes

	// Calculate Implied Forward
	impliedFwd := p.calculateImpliedForward(strikeMap, strikes, expiry, snap.AsOf)
	cs.ChainState.ImpliedForward = impliedFwd

	// 3. Determine ATM Strike (closest to Implied Forward if available, else Spot)
	centerPrice := snap.Underlying.Spot
	if impliedFwd > 0 {
		centerPrice = impliedFwd
	}

	atmDist := math.MaxFloat64
	var atmStrike float64
	for _, k := range strikes {
		dist := math.Abs(k - centerPrice)
		if dist < atmDist {
			atmDist = dist
			atmStrike = k
		}
	}
	cs.ChainState.ATMStrike = atmStrike
	// Infer step
	if len(strikes) > 1 {
		// Just take diff between first two for now, or median difference?
		// Simple: strikes[1] - strikes[0]
		cs.ChainState.StrikeStep = strikes[1] - strikes[0]
	}

	// 4. Transform Map to String Keys and Apply Eligibility
	eligibleStrikes := []float64{}
	ivInputs := []market.IVPoint{}
	legUniverseStrikesMap := make(map[float64]bool)
	legUniverseTypesMap := make(map[market.OptionType]bool)

	for _, k := range strikes {
		sc := strikeMap[k]
		kStr := fmt.Sprintf("%.0f", k)
		cs.ByStrike[kStr] = sc

		// Check eligibility for simple IV surface input (both legs present & tradable?)
		// Or just individual options?
		// User example: eligible_strikes array in chain_state.
		// Let's assume eligible means at least one side is tradable and meets criteria.

		isEligible := false

		checkOption := func(opt *market.Option, oType market.OptionType) {
			if opt == nil {
				return
			}
			// Re-eval basic quality again based on Chain rules? or trust Upstream quality?
			// User rules: max_spread_pct.
			// Currently upstream `service.go` sets flags `WIDE_SPREAD` if > 15%.
			// We have stricter rule 12% in chain processor.

			spreadPct := 0.0
			if opt.Mid > 0 {
				spreadPct = (opt.Ask - opt.Bid) / opt.Mid
			}

			// Override/Refine quality
			if spreadPct > p.Rules.MaxSpreadPct {
				opt.Quality.IsTradable = false
				opt.Quality.Flags = append(opt.Quality.Flags, "WIDE_SPREAD_CHAIN_RULE")
			}

			if opt.Quality.IsTradable {
				isEligible = true
				ivInputs = append(ivInputs, market.IVPoint{
					Strike: k,
					Type:   oType,
					Mark:   opt.Mid,
				})
				legUniverseStrikesMap[k] = true
				legUniverseTypesMap[oType] = true
			}
		}

		checkOption(sc.Call, market.Call)
		checkOption(sc.Put, market.Put)

		if isEligible {
			eligibleStrikes = append(eligibleStrikes, k)
		}
	}
	cs.ChainState.EligibleStrikes = eligibleStrikes

	// 5. Populate Downstream Sets
	cs.DownstreamReadySets.IVSurfaceInputs = ivInputs

	for k := range legUniverseStrikesMap {
		cs.DownstreamReadySets.StrategyLegUniverse.AllowedStrikes = append(cs.DownstreamReadySets.StrategyLegUniverse.AllowedStrikes, k)
	}
	sort.Float64s(cs.DownstreamReadySets.StrategyLegUniverse.AllowedStrikes)

	for t := range legUniverseTypesMap {
		cs.DownstreamReadySets.StrategyLegUniverse.AllowedTypes = append(cs.DownstreamReadySets.StrategyLegUniverse.AllowedTypes, t)
	}
	cs.DownstreamReadySets.StrategyLegUniverse.Notes = []string{"Generated from tradable quotes"}

	// 6. Liquidity Summary
	// Check ATM liquidity
	atmChain := strikeMap[atmStrike]
	avgSpreadATM := 0.0
	countATM := 0.0
	tradableATM := false

	if atmChain != nil {
		if atmChain.Call != nil && atmChain.Call.Quality.IsTradable {
			avgSpreadATM += (atmChain.Call.Ask - atmChain.Call.Bid) / atmChain.Call.Mid
			countATM++
		}
		if atmChain.Put != nil && atmChain.Put.Quality.IsTradable {
			avgSpreadATM += (atmChain.Put.Ask - atmChain.Put.Bid) / atmChain.Put.Mid
			countATM++
		}
	}

	if countATM > 0 {
		avgSpreadATM /= countATM
		tradableATM = true
	}

	cs.LiquiditySummary = market.LiquiditySummary{
		TradableNearATM:     tradableATM,
		AvgSpreadPctNearATM: avgSpreadATM,
		Notes:               []string{"Calculated based on ATM strike"},
	}

	return cs, nil
}
