package main

import (
	"fmt"
	"io"
	"log"

	"math"
	"os"

	"time"

	"volatility-engine/internal/chain"
	"volatility-engine/internal/config"
	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/ingest"

	"volatility-engine/internal/quant/intel"

	"volatility-engine/internal/quant/iv"

	"volatility-engine/internal/quant/regime"
	"volatility-engine/internal/quant/strategy"

	"sort"
	"sync"
	"volatility-engine/internal/quant/pricing"
	"volatility-engine/internal/quant/surface"
	"volatility-engine/internal/service/report"
)

func main() {
	// Setup File Logging
	logFile, err := os.OpenFile("volengine.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	defer logFile.Close()

	// Write to both stdout and file
	mw := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(mw)

	log.Println("Starting Volatility Engine...")

	// Basic run loop placeholder
	if len(os.Args) > 1 {
		cmd := os.Args[1]
		switch cmd {
		case "snapshot-run":

			runSnapshotPipeline()
		default:
			log.Fatalf("Unknown command: %s", cmd)
		}
	} else {
		// Default behavior or help
		fmt.Println("Usage: volengine <command>")
		fmt.Println("Commands:")
		fmt.Println("  snapshot-run    Run the pipeline on a single snapshot")
	}
}

func runSnapshotPipeline() {
	log.Println("Running snapshot pipeline...")

	// Load configuration from file
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Loaded config: %s @ %.2f, Risk-Free Rate: %.2f%%",
		cfg.Market.Underlying, cfg.Market.Spot, cfg.Pricing.RiskFreeRate*100)

	// Use config values
	underlying := cfg.Market.Underlying
	spot := cfg.Market.Spot

	// Parse file configurations from config
	var fileConfigs []struct {
		Path   string
		Expiry time.Time
	}

	for _, f := range cfg.Files {
		expiry, err := cfg.ParseFileExpiry(f.Expiry)
		if err != nil {
			log.Fatalf("Failed to parse expiry for %s: %v", f.Path, err)
		}
		fileConfigs = append(fileConfigs, struct {
			Path   string
			Expiry time.Time
		}{
			Path:   f.Path,
			Expiry: expiry.UTC(),
		})
	}

	// 1. Multi-File Ingest and Merge
	ingestSvc := ingest.NewIngestService()

	var allQuotes []market.Quote
	expiryMap := make(map[string]market.ExpiryMetadata)

	log.Printf("Ingesting %d CSV file(s)...", len(fileConfigs))

	for i, config := range fileConfigs {
		s3Key := fmt.Sprintf("s3://vol-engine/raw/prototypes/%s_%s_%d.csv",
			underlying, time.Now().Format("20060102"), i)

		snap, err := ingestSvc.IngestSnapshot(config.Path, underlying, spot, config.Expiry, s3Key)
		if err != nil {
			log.Printf("Warning: Failed to ingest %s: %v", config.Path, err)
			continue
		}

		log.Printf("  [%d/%d] Ingested: %s (%d quotes, expiry: %s)",

			i+1, len(fileConfigs), config.Path, len(snap.Quotes),
			config.Expiry.Format("2006-01-02"))

		// Merge quotes
		allQuotes = append(allQuotes, snap.Quotes...)

		// Merge expiries (deduplicate by expiry date)
		for _, exp := range snap.Expiries {
			key := exp.Expiry.Format("2006-01-02")
			if _, exists := expiryMap[key]; !exists {
				expiryMap[key] = exp
			}
		}
	}

	// Build merged expiries array
	var mergedExpiries []market.ExpiryMetadata
	for _, exp := range expiryMap {
		mergedExpiries = append(mergedExpiries, exp)
	}

	// Create merged snapshot
	snap := &market.Snapshot{
		ID:         fmt.Sprintf("snap_%s_%s", time.Now().Format("20060102_150405"), underlying),
		AsOf:       time.Now().UTC(),
		Underlying: market.Underlying{Symbol: underlying, Spot: spot},
		Quotes:     allQuotes,
		Expiries:   mergedExpiries,
	}

	// Recalculate quality summary
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

	log.Printf("Merged Snapshot: %s", snap.ID)
	log.Printf("Underlying: %s, Spot: %.2f", snap.Underlying.Symbol, snap.Underlying.Spot)
	log.Printf("Total Quotes: %d (from %d file(s))", len(snap.Quotes), len(fileConfigs))
	log.Printf("Total Expiries: %d", len(snap.Expiries))
	log.Printf("Tradable: %d, Rejected: %d", snap.QualitySummary.TradableQuotes, snap.QualitySummary.RejectedQuotes)

	// Print a few sample quotes to prove it worked
	if len(snap.Quotes) > 0 {
		log.Println("Sample Quote (first 200):")
		for i := 0; i < len(snap.Quotes) && i < 1; i++ {
			q := snap.Quotes[i]
			log.Printf(" - %s %.2f %s: Mid=%.2f, Flags=%v",
				q.Contract.Symbol, q.Contract.Strike, q.Contract.Type, q.Market.Mid, q.Quality.Flags)
		}
	}

	// Dump full JSON for verification
	// bytes, _ := json.MarshalIndent(snap, "", "  ")
	// fmt.Println(string(bytes))

	// 2. Multi-Expiry Pipeline
	log.Println("Starting Multi-Expiry Pipeline...")

	// Shared Generators/Solvers
	chainProc := chain.NewProcessor(cfg.Pricing.RiskFreeRate)
	ivSolver := iv.NewSolver(iv.DefaultSettings())
	surfaceBuilder := surface.NewBuilder(surface.DefaultSettings())
	intelEngine := intel.NewEngine()
	selector := strategy.NewSelector()

	// Container for 3-Pass Architecture
	var expiryContexts []*market.ExpiryContext
	var mu sync.Mutex
	var wg sync.WaitGroup

	log.Println("=== Pass 1: Per-Expiry Context Construction (Parallel) ===")
	log.Printf("Found %d expiries to process", len(snap.Expiries))

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

			// Capture result helper
			finish := func(reason string) {
				if reason != "" {
					ctx.SkipReason = reason
					log.Printf(" > [SKIP] %s: %s", loopExpiry.Format("2006-01-02"), reason)
				}
				mu.Lock()
				expiryContexts = append(expiryContexts, ctx)
				mu.Unlock()
			}

			// Level 1 Gating: DTE
			if eMeta.DaysToExpiry < 2 {
				finish("DTE < 2 (Microstructure Noise)")
				return
			}

			// A. Chain Generation
			chainSnap, err := chainProc.GenerateChain(snap, loopExpiry)
			if err != nil {
				finish(fmt.Sprintf("Chain Gen Failed: %v", err))
				return
			}
			ctx.ChainSnapshot = chainSnap

			// B. Forward Generation
			fwdState := chainProc.GenerateForwardState(snap, chainSnap, loopExpiry)
			ctx.ForwardState = fwdState

			// C. Determine S_input for IV
			S_input := fwdState.Forward.Mid
			if S_input <= 0 {
				if chainSnap.ChainState.ImpliedForward > 0 {
					S_input = chainSnap.ChainState.ImpliedForward
				} else {
					S_input = snap.Underlying.Spot
				}
			}

			// TTE and DF
			tte := eMeta.TTEYears
			if tte <= 0 {
				tte = chainSnap.Expiry.TTEYears
			}
			df := math.Exp(-cfg.Pricing.RiskFreeRate * tte)

			// D. IV Solving
			var surfacePoints []market.IVPoint

			// 1. Adaptive heuristics
			window := cfg.Selection.LogMoneynessWindow
			minPts := cfg.Selection.MinPointsPerExpiry
			isFarTenor := eMeta.DaysToExpiry > 21

			if isFarTenor {
				window = window * 1.5
				if minPts > 15 {
					minPts = 15
				}
			}

			// Pre-filter inputs
			var filteredInputs []market.IVPoint
			for _, ivInput := range chainSnap.DownstreamReadySets.IVSurfaceInputs {
				m := math.Log(ivInput.Strike / S_input)
				if math.Abs(m) <= window {
					filteredInputs = append(filteredInputs, ivInput)
				}
			}

			// Sort by distance from ATM
			sort.Slice(filteredInputs, func(i, j int) bool {
				distI := math.Abs(math.Log(filteredInputs[i].Strike / S_input))
				distJ := math.Abs(math.Log(filteredInputs[j].Strike / S_input))
				return distI < distJ
			})

			// Cap inputs
			maxPts := cfg.Selection.MaxPointsPerExpiry
			if len(filteredInputs) > maxPts {
				filteredInputs = filteredInputs[:maxPts]
			}

			// Solve
			for _, ivInput := range filteredInputs {
				kStr := fmt.Sprintf("%.0f", ivInput.Strike)
				sc := chainSnap.ByStrike[kStr]
				if sc == nil {
					continue
				}

				// Find marks
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
						RiskFreeRateCC: cfg.Pricing.RiskFreeRate,
						DiscountFactor: df,
					},
					Settings: iv.DefaultSettings(), // Solver settings
				}

				ivRes := ivSolver.Solve(ivReq)

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

			// Skew Gating (Level 2)
			if len(surfacePoints) >= minPts {
				skewSnap := surfaceBuilder.BuildSkew(loopExpiry.Format("2006-01-02"), surfacePoints, S_input, tte)
				ctx.SkewSnapshot = &skewSnap
				ctx.Stage = "SkewReady"
				log.Printf(" > [OK] %s | Pts: %d | ATM: %.2f%%", loopExpiry.Format("2006-01-02"), len(surfacePoints), skewSnap.Metrics.ATMVol*100)
			} else {
				ctx.Stage = "NoSkew"
				ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("Not enough points: %d < %d", len(surfacePoints), minPts))
				log.Printf(" > [PARTIAL] %s | Pts: %d (Skew Skipped)", loopExpiry.Format("2006-01-02"), len(surfacePoints))
			}

			finish("") // Success (or partial success)

		}(expMeta)
	}

	wg.Wait()

	// === Pass 2: Surface Construction & Regime (Serial) ===
	log.Println("=== Pass 2: Cross-Expiry Regime Detection ===")

	// Sort contexts by TTE (Determinism)
	sort.Slice(expiryContexts, func(i, j int) bool {
		return expiryContexts[i].Expiry.DaysToExpiry < expiryContexts[j].Expiry.DaysToExpiry
	})

	// Collect valid skews for surface
	var surfaceSkews []market.IVSkewSnapshot
	for _, ctx := range expiryContexts {
		if ctx.SkewSnapshot != nil {
			surfaceSkews = append(surfaceSkews, *ctx.SkewSnapshot)
		}
	}

	if len(surfaceSkews) == 0 {
		log.Fatal("Critical Error: No valid skews generated. Cannot build surface.")
	}

	surfaceSnap := market.IVSurfaceSnapshot{
		AsOf:       snap.AsOf,
		Underlying: snap.Underlying.Symbol,
		Skews:      surfaceSkews,
	}

	// Regime Detector
	ivHistoryProvider := &regime.CSVIVHistoryProvider{BaseDir: "data/history"}
	priceHistoryProvider := &regime.CSVPriceHistoryProvider{BaseDir: "data/history"}

	detector := regime.NewDetector(regime.DefaultSettings(), ivHistoryProvider, priceHistoryProvider)
	regimeState := detector.Detect(surfaceSnap)

	log.Printf("Regime: %s | Bias: %s | Score: %.2f", regimeState.Decision.Regime, regimeState.Decision.Bias, regimeState.Decision.Score)
	log.Printf("IV30: %.2f%% (Rank: %.2f)", regimeState.IVReference.IV*100, regimeState.HistoricalContext.IVRank)

	// Persist IV30
	if regimeState.IVReference.IV > 0 {
		_ = ivHistoryProvider.RecordIV30(snap.Underlying.Symbol, snap.AsOf, regimeState.IVReference.IV, regimeState.IVReference.Confidence, regimeState.IVReference.Method)
	}

	// === Pass 3: Intel & Strategy (Per-Expiry) ===
	log.Println("=== Pass 3: Intel & Strategy Generation (Per-Expiry) ===")

	for _, ctx := range expiryContexts {
		if ctx.SkipReason != "" || ctx.SkewSnapshot == nil {
			continue // Need skew for deeper analysis usually
		}

		// A. Intel
		intelSnap := intelEngine.ComputeIntel(ctx.ChainSnapshot, &surfaceSnap)
		ctx.IntelSnapshot = intelSnap

		// Log Intel Signals
		if len(intelSnap.Signals) > 0 {
			log.Printf(" > [INTEL] %s Signals:", ctx.Expiry.Expiry.Format("2006-01-02"))
			for _, sig := range intelSnap.Signals {
				log.Printf("    - [%s] (Score: %.2f) %s", sig.Name, sig.Score, sig.Explanation)
			}
		}

		// Log Intel Explanation
		// log.Printf(" > [INTEL] Explanation: %s", strings.Join(intelSnap.Explanation, " | "))

		// B. Strategies
		cands, err := selector.SelectStrategies(&regimeState, &surfaceSnap, intelSnap, ctx.ChainSnapshot, ctx.ForwardState)
		if err != nil {
			ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("Strategy Gen Failed: %v", err))
		} else {
			ctx.StrategyCandidates = cands
		}

		ctx.Stage = "Analyzed"
		log.Printf(" > Analyzed %s: %d Signals, %d Candidates", ctx.Expiry.Expiry.Format("2006-01-02"), len(intelSnap.Signals), len(cands))
	}

	// === Final: Aggregation & Ranking ===
	log.Println("=== Final Selection ===")

	var allCandidates []market.StrategyCandidate
	for _, ctx := range expiryContexts {
		if len(ctx.StrategyCandidates) > 0 {
			allCandidates = append(allCandidates, ctx.StrategyCandidates...)
		}
	}

	// Global Rank
	sort.Slice(allCandidates, func(i, j int) bool {
		return allCandidates[i].Quality.Score > allCandidates[j].Quality.Score
	})

	log.Printf("Generated Total %d Candidates. Top Recommendations:", len(allCandidates))
	topN := 5
	for i := 0; i < len(allCandidates) && i < topN; i++ {
		cand := allCandidates[i]
		log.Printf(" #%d [%s] %s (%s) Score=%.2f | Expiry: %s",
			i+1, cand.StrategyType, cand.ID, cand.Intent, cand.Quality.Score, cand.Expiry)
		log.Printf("    Entry: %s %.2f", cand.Entry.PremiumType, cand.Entry.NetPremium)
		log.Printf("    Rationale:")
		log.Printf("     - Basis: %s", cand.Rationale.SelectionBasis)
		log.Printf("     - Thesis: %s", cand.Rationale.Thesis)
		log.Printf("     - Legs: %s", cand.Rationale.LegSelection)

		// Find the Skew Snapshot for this candidate's expiry to calculate Fair Value
		var candSkew *market.IVSkewSnapshot
		for _, s := range surfaceSnap.Skews {
			if s.Expiry == cand.Expiry {
				candSkew = &s
				break
			}
		}

		for _, leg := range cand.Legs {
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
					r := cfg.Pricing.RiskFreeRate

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

	// === Risk Reporting ===
	if len(allCandidates) > 0 {
		log.Println("=== Generating Risk Reports ===")

		// Take top N config
		limit := cfg.Report.TopN
		if limit > len(allCandidates) {
			limit = len(allCandidates)
		}
		topCandidates := allCandidates[:limit]

		rptGen := report.NewGenerator(*cfg)

		meta := struct {
			Regime string
			IV30   float64
			IVRank float64
		}{
			Regime: regimeState.Decision.Regime,
			IV30:   regimeState.IVReference.IV,
			IVRank: regimeState.HistoricalContext.IVRank,
		}

		err := rptGen.GenerateBatch(snap.ID, topCandidates, nil, meta, cfg.Pricing.RiskFreeRate)
		if err != nil {
			log.Printf("Error generating risk reports: %v", err)
		} else {
			log.Printf("Reports saved to %s/%s/", cfg.Report.OutDir, snap.ID)
		}
	}

	log.Println("Pipeline finished.")
}
