package app

import (
	"fmt"
	"log"
	"math"
	"sort"
	"sync"

	"volatility-engine/internal/chain"
	"volatility-engine/internal/config"
	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/ingest"
	"volatility-engine/internal/quant/intel"
	"volatility-engine/internal/quant/iv"
	"volatility-engine/internal/quant/pricing"
	"volatility-engine/internal/quant/regime"
	"volatility-engine/internal/quant/strategy"
	"volatility-engine/internal/quant/surface"
	"volatility-engine/internal/service/report"
)

type Engine struct {
	cfg *config.Config

	ingestSvc      *ingest.IngestService
	chainProc      *chain.Processor
	ivSolver       *iv.Solver
	surfaceBuilder *surface.Builder
	intelEngine    *intel.Engine
	selector       *strategy.Selector
}

func NewEngine(cfg *config.Config) *Engine {
	return &Engine{
		cfg:            cfg,
		ingestSvc:      ingest.NewIngestService(),
		chainProc:      chain.NewProcessor(cfg.Pricing.RiskFreeRate),
		ivSolver:       iv.NewSolver(iv.DefaultSettings()),
		surfaceBuilder: surface.NewBuilder(surface.DefaultSettings()),
		intelEngine:    intel.NewEngine(),
		selector:       strategy.NewSelector(),
	}
}

// RunSnapshot builds merged snapshot + runs your 3-pass pipeline + returns candidates and state.
func (e *Engine) RunSnapshot(in SnapshotInput) (*EngineOutput, error) {
	cfg := e.cfg

	underlying := in.Underlying
	spot := in.Spot
	asOf := in.AsOf.UTC()
	r := in.RiskFreeRate
	if r == 0 {
		r = cfg.Pricing.RiskFreeRate
	}

	// 1) Ingest and merge quotes
	var allQuotes []market.Quote
	expiryMap := make(map[string]market.ExpiryMetadata)

	log.Printf("Ingesting %d CSV file(s)...", len(in.Files))
	for i, fc := range in.Files {
		s3Key := fmt.Sprintf("s3://vol-engine/replay/%s_%s_%d.csv",
			underlying, asOf.Format("20060102_150405"), i)

		snapPart, err := e.ingestSvc.IngestSnapshot(fc.Path, underlying, spot, fc.Expiry.UTC(), s3Key)
		if err != nil {
			log.Printf("Warning: Failed to ingest %s: %v", fc.Path, err)
			continue
		}

		allQuotes = append(allQuotes, snapPart.Quotes...)
		for _, exp := range snapPart.Expiries {
			key := exp.Expiry.Format("2006-01-02")
			if _, exists := expiryMap[key]; !exists {
				expiryMap[key] = exp
			}
		}
	}

	var mergedExpiries []market.ExpiryMetadata
	for _, exp := range expiryMap {
		mergedExpiries = append(mergedExpiries, exp)
	}

	// Stable snapshot ID (important for replay determinism)
	snapID := in.SnapshotID
	if snapID == "" {
		snapID = fmt.Sprintf("snap_%s_%s", asOf.Format("20060102_150405"), underlying)
	}

	snap := &market.Snapshot{
		ID:         snapID,
		AsOf:       asOf,
		Underlying: market.Underlying{Symbol: underlying, Spot: spot},
		Quotes:     allQuotes,
		Expiries:   mergedExpiries,
	}

	// quality summary
	tradable := 0
	rejected := 0
	for _, q := range allQuotes {
		if len(q.Quality.Flags) == 0 {
			tradable++
		} else {
			rejected++
		}
	}
	snap.QualitySummary = market.QualitySummary{
		TotalQuotes:    len(allQuotes),
		TradableQuotes: tradable,
		RejectedQuotes: rejected,
	}

	// 2) Multi-expiry pipeline (your same logic)
	var expiryContexts []*market.ExpiryContext
	var mu sync.Mutex
	var wg sync.WaitGroup

	log.Println("=== Pass 1: Per-Expiry Context Construction (Parallel) ===")
	for _, expMeta := range snap.Expiries {
		wg.Add(1)
		go func(eMeta market.ExpiryMetadata) {
			defer wg.Done()

			loopExpiry := eMeta.Expiry
			ctx := &market.ExpiryContext{
				ID:     fmt.Sprintf("ctx_%s", loopExpiry.Format("20060102")),
				Expiry: eMeta,
				AsOf:   snap.AsOf,
				Stage:  "Initialized",
			}

			finish := func(reason string) {
				if reason != "" {
					ctx.SkipReason = reason
					log.Printf(" > [SKIP] %s: %s", loopExpiry.Format("2006-01-02"), reason)
				}
				mu.Lock()
				expiryContexts = append(expiryContexts, ctx)
				mu.Unlock()
			}

			if eMeta.DaysToExpiry < 2 {
				finish("DTE < 2 (Microstructure Noise)")
				return
			}

			chainSnap, err := e.chainProc.GenerateChain(snap, loopExpiry)
			if err != nil {
				finish(fmt.Sprintf("Chain Gen Failed: %v", err))
				return
			}
			ctx.ChainSnapshot = chainSnap

			fwdState := e.chainProc.GenerateForwardState(snap, chainSnap, loopExpiry)
			ctx.ForwardState = fwdState

			S_input := fwdState.Forward.Mid
			if S_input <= 0 {
				if chainSnap.ChainState.ImpliedForward > 0 {
					S_input = chainSnap.ChainState.ImpliedForward
				} else {
					S_input = snap.Underlying.Spot
				}
			}

			tte := eMeta.TTEYears
			if tte <= 0 {
				tte = chainSnap.Expiry.TTEYears
			}
			df := math.Exp(-r * tte)

			// Adaptive heuristics
			window := cfg.Selection.LogMoneynessWindow
			minPts := cfg.Selection.MinPointsPerExpiry
			if eMeta.DaysToExpiry > 21 {
				window *= 1.5
				if minPts > 15 {
					minPts = 15
				}
			}

			var filteredInputs []market.IVPoint
			for _, ivInput := range chainSnap.DownstreamReadySets.IVSurfaceInputs {
				m := math.Log(ivInput.Strike / S_input)
				if math.Abs(m) <= window {
					filteredInputs = append(filteredInputs, ivInput)
				}
			}

			sort.Slice(filteredInputs, func(i, j int) bool {
				di := math.Abs(math.Log(filteredInputs[i].Strike / S_input))
				dj := math.Abs(math.Log(filteredInputs[j].Strike / S_input))
				return di < dj
			})

			maxPts := cfg.Selection.MaxPointsPerExpiry
			if len(filteredInputs) > maxPts {
				filteredInputs = filteredInputs[:maxPts]
			}

			var surfacePoints []market.IVPoint

			for _, ivInput := range filteredInputs {
				kStr := fmt.Sprintf("%.0f", ivInput.Strike)
				sc := chainSnap.ByStrike[kStr]
				if sc == nil {
					continue
				}

				var opt *market.Option
				var pairOpt *market.Option
				if ivInput.Type == market.Call {
					opt = sc.Call
					pairOpt = sc.Put
				} else {
					opt = sc.Put
					pairOpt = sc.Call
				}

				if opt == nil || opt.Mid <= 0 {
					continue
				}

				pmid, pbid, pask := 0.0, 0.0, 0.0
				if pairOpt != nil {
					pmid = pairOpt.Mid
					pbid = pairOpt.Bid
					pask = pairOpt.Ask
				}

				ivReq := market.IVRequest{
					ID:   fmt.Sprintf("iv_%s_%.0f_%s", loopExpiry.Format("20060102"), ivInput.Strike, ivInput.Type),
					AsOf: snap.AsOf,
					Instrument: market.IVInstrument{
						Underlying: snap.Underlying.Symbol,
						Expiry:     loopExpiry.Format("2006-01-02"),
						Strike:     ivInput.Strike,
						Type:       ivInput.Type,
					},
					Market: market.IVMarket{
						MarkPrice:    opt.Mid,
						MarkSource:   "MID",
						Bid:          opt.Bid,
						Ask:          opt.Ask,
						Volume:       int(opt.Volume),
						OpenInterest: int(opt.OpenInterest),
						SpreadPct:    (opt.Ask - opt.Bid) / opt.Mid,
						PairedMid:    pmid,
						PairedBid:    pbid,
						PairedAsk:    pask,
					},
					Context: market.IVContext{
						Spot:          snap.Underlying.Spot,
						Forward:       S_input,
						ForwardSource: "HYBRID",
						TTEYears:      tte,
					},
					Rates: market.Rates{
						RiskFreeRateCC: r,
						DiscountFactor: df,
					},
					Settings: iv.DefaultSettings(),
				}

				ivRes := e.ivSolver.Solve(ivReq)
				if ivRes.Status == market.IVStatusConverged {
					p := market.IVPoint{
						Expiry:       ivRes.Instrument.Expiry,
						Strike:       ivRes.Instrument.Strike,
						Type:         ivRes.Instrument.Type,
						ImpliedVol:   ivRes.Result.ImpliedVol,
						Confidence:   ivRes.Quality.Confidence,
						MarkSource:   ivRes.Market.MarkSource,
						MarkPrice:    ivRes.Market.MarkPrice,
						FitErrorAbs:  ivRes.Result.FitErrorAbs,
						VegaPerPoint: ivRes.Diagnostics.VegaAtSolution,
						LogMoneyness: math.Log(ivInput.Strike / S_input),
					}
					surfacePoints = append(surfacePoints, p)
				}
			}

			ctx.IVPoints = surfacePoints

			if len(surfacePoints) >= minPts {
				skewSnap := e.surfaceBuilder.BuildSkew(loopExpiry.Format("2006-01-02"), surfacePoints, S_input, tte)
				ctx.SkewSnapshot = &skewSnap
				ctx.Stage = "SkewReady"
			} else {
				ctx.Stage = "NoSkew"
			}

			finish("")
		}(expMeta)
	}

	wg.Wait()

	// Pass 2: surface + regime
	sort.Slice(expiryContexts, func(i, j int) bool {
		return expiryContexts[i].Expiry.DaysToExpiry < expiryContexts[j].Expiry.DaysToExpiry
	})

	var surfaceSkews []market.IVSkewSnapshot
	for _, ctx := range expiryContexts {
		if ctx.SkewSnapshot != nil {
			surfaceSkews = append(surfaceSkews, *ctx.SkewSnapshot)
		}
	}
	if len(surfaceSkews) == 0 {
		return nil, fmt.Errorf("no valid skews generated")
	}

	surfaceSnap := market.IVSurfaceSnapshot{
		AsOf:       snap.AsOf,
		Underlying: snap.Underlying.Symbol,
		Skews:      surfaceSkews,
	}

	ivHistoryProvider := &regime.CSVIVHistoryProvider{BaseDir: "data/history"}
	priceHistoryProvider := &regime.CSVPriceHistoryProvider{BaseDir: "data/history"}
	detector := regime.NewDetector(regime.DefaultSettings(), ivHistoryProvider, priceHistoryProvider)

	regimeState := detector.Detect(surfaceSnap)

	// Persist IV30 (same behavior)
	if regimeState.IVReference.IV > 0 {
		_ = ivHistoryProvider.RecordIV30(snap.Underlying.Symbol, snap.AsOf, regimeState.IVReference.IV, regimeState.IVReference.Confidence, regimeState.IVReference.Method)
	}

	// Pass 3: intel + candidates
	for _, ctx := range expiryContexts {
		if ctx.SkipReason != "" || ctx.SkewSnapshot == nil {
			continue
		}
		intelSnap := e.intelEngine.ComputeIntel(ctx.ChainSnapshot, &surfaceSnap)
		ctx.IntelSnapshot = intelSnap

		cands, err := e.selector.SelectStrategies(&regimeState, &surfaceSnap, intelSnap, ctx.ChainSnapshot, ctx.ForwardState)
		if err == nil {
			ctx.StrategyCandidates = cands
		}
	}

	var allCandidates []market.StrategyCandidate
	for _, ctx := range expiryContexts {
		allCandidates = append(allCandidates, ctx.StrategyCandidates...)
	}

	sort.Slice(allCandidates, func(i, j int) bool {
		return allCandidates[i].Quality.Score > allCandidates[j].Quality.Score
	})

	// (optional) risk reports — keep same generator call, but let caller decide
	_ = pricing.Calculate // keep import used if needed later

	// Example: if you still want the engine to auto-write reports, you can keep it here.
	// Better: let snapshot-run do it; replay-run usually doesn’t want heavy reports each step.

	return &EngineOutput{
		Snapshot:       snap,
		ExpiryContexts: expiryContexts,
		Surface:        surfaceSnap,
		Regime:         regimeState,
		Candidates:     allCandidates,
	}, nil
}

// Utility if snapshot-run still wants risk reports:
func GenerateRiskReports(cfg *config.Config, snapID string, candidates []market.StrategyCandidate, surfaceSnap *market.IVSurfaceSnapshot, regimeStr string, iv30, ivRank float64, r float64) error {
	if len(candidates) == 0 {
		return nil
	}
	limit := cfg.Report.TopN
	if limit > len(candidates) {
		limit = len(candidates)
	}
	topCandidates := candidates[:limit]

	rptGen := report.NewGenerator(*cfg)
	meta := struct {
		Regime string
		IV30   float64
		IVRank float64
	}{
		Regime: regimeStr,
		IV30:   iv30,
		IVRank: ivRank,
	}

	return rptGen.GenerateBatch(snapID, topCandidates, surfaceSnap, meta, r)
}
