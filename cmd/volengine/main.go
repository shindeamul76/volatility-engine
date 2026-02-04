package main

import (
	"encoding/json"
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

	// "volatility-engine/internal/quant/intel"
	"volatility-engine/internal/quant/iv"

	"volatility-engine/internal/quant/regime"
	// "volatility-engine/internal/quant/strategy"

	"sort"
	"sync"
	"volatility-engine/internal/quant/surface"
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

	var surfaceSkews []market.IVSkewSnapshot
	var mu sync.Mutex
	var wg sync.WaitGroup

	log.Printf("Found %d expiries to process", len(snap.Expiries))

	for _, expMeta := range snap.Expiries {
		wg.Add(1)
		go func(eMeta market.ExpiryMetadata) {
			defer wg.Done()

			loopExpiry := eMeta.Expiry
			// Skip DTE < 2 (microstructure makes IV unreliable)
			if eMeta.DaysToExpiry < 2 {
				log.Printf(" > Skipping Expiry %s: DTE below 2 days threshold", loopExpiry.Format("2006-01-02"))
				return
			}
			// log.Printf("Processing Expiry: %s (TTE: %.4f)", loopExpiry.Format("2006-01-02"), eMeta.TTEYears)

			// A. Chain Generation
			chainSnap, err := chainProc.GenerateChain(snap, loopExpiry)
			if err != nil {
				log.Printf("Error generating chain for %s: %v", loopExpiry, err)
				return
			}

			// B. Forward Generation
			fwdState := chainProc.GenerateForwardState(snap, chainSnap, loopExpiry)

			// C. Determine S_input
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

			// 1. Adaptive heuristics based on TTE
			window := cfg.Selection.LogMoneynessWindow
			minPts := cfg.Selection.MinPointsPerExpiry
			isFarTenor := eMeta.DaysToExpiry > 21

			if isFarTenor {
				// Widen window for far tenors to capture more liquid points
				window = window * 1.5
				// Relax min points slightly to help bracketing IV30
				if minPts > 15 {
					minPts = 15
				}
			}

			// Pre-filter by log-moneyness window
			var filteredInputs []market.IVPoint
			for _, ivInput := range chainSnap.DownstreamReadySets.IVSurfaceInputs {
				m := math.Log(ivInput.Strike / S_input)
				if math.Abs(m) <= window {
					filteredInputs = append(filteredInputs, ivInput)
				}
			}

			// 2. Sort by distance from ATM (prioritize near-ATM strikes if capping is needed)
			sort.Slice(filteredInputs, func(i, j int) bool {
				distI := math.Abs(math.Log(filteredInputs[i].Strike / S_input))
				distJ := math.Abs(math.Log(filteredInputs[j].Strike / S_input))
				return distI < distJ
			})

			// 3. Cap the points to process
			maxPts := cfg.Selection.MaxPointsPerExpiry
			if len(filteredInputs) > maxPts {
				filteredInputs = filteredInputs[:maxPts]
			}

			// 4. Solve IV for selected strikes
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
						RiskFreeRateCC: cfg.Pricing.RiskFreeRate,
						DiscountFactor: df,
					},
					Settings: iv.DefaultSettings(),
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

			// E. Build Skew (with liquidity guard)
			if len(surfacePoints) >= minPts {
				skewSnap := surfaceBuilder.BuildSkew(loopExpiry.Format("2006-01-02"), surfacePoints, S_input, tte)

				mu.Lock()
				surfaceSkews = append(surfaceSkews, skewSnap)
				mu.Unlock()

				msg := "Valid"
				if isFarTenor {
					msg = "Valid (Far)"
				}
				log.Printf(" > %s: %s | ATM IV: %.2f%% | Pts: %d", msg, loopExpiry.Format("2006-01-02"), skewSnap.Metrics.ATMVol*100, len(surfacePoints))
			} else {
				log.Printf(" > Skipped: %s | Only %d valid points (min %d required)",
					loopExpiry.Format("2006-01-02"), len(surfacePoints), minPts)
			}

		}(expMeta)
	}

	wg.Wait()

	// Sort Skews by TTE
	sort.Slice(surfaceSkews, func(i, j int) bool {
		return surfaceSkews[i].TTEYears < surfaceSkews[j].TTEYears
	})

	// 6. Regime Detection

	// log.Println("=== Regime Detection ===")

	// Create a surface snapshot wrapper
	surfaceSnap := market.IVSurfaceSnapshot{
		AsOf:       snap.AsOf,
		Underlying: snap.Underlying.Symbol,
		Skews:      surfaceSkews,
	}

	// Use concrete type for persistence
	ivHistoryProvider := &regime.CSVIVHistoryProvider{BaseDir: "data/history"}
	priceHistoryProvider := &regime.CSVPriceHistoryProvider{BaseDir: "data/history"}

	detector := regime.NewDetector(
		regime.DefaultSettings(),
		ivHistoryProvider,
		priceHistoryProvider,
	)

	regimeState := detector.Detect(surfaceSnap)

	log.Printf("Regime: %s (Bias: %s)", regimeState.Decision.Regime, regimeState.Decision.Bias)
	log.Printf("Score: %.2f", regimeState.Decision.Score)
	log.Printf("Reference IV (30d): %.2f%% (Conf: %.2f, Method: %s)",
		regimeState.IVReference.IV*100, regimeState.IVReference.Confidence, regimeState.IVReference.Method)
	log.Printf("Historical Context: Rank=%.2f, Pct=%.2f", regimeState.HistoricalContext.IVRank, regimeState.HistoricalContext.IVPercentile)
	log.Printf("Realized Vol (HV20): %.2f%% (Ratio: %.2f)", regimeState.RealizedVol.HV*100, regimeState.RealizedVol.IVHVRatio)

	// Persist today's IV30
	if regimeState.IVReference.IV > 0 {
		err := ivHistoryProvider.RecordIV30(
			snap.Underlying.Symbol,
			snap.AsOf, // This is "Now" (or snapshot time)
			regimeState.IVReference.IV,
			regimeState.IVReference.Confidence,
			regimeState.IVReference.Method,
		)
		if err != nil {
			log.Printf("[WARN] Failed to persist IV30 history: %v", err)
		} else {
			log.Printf("[INFO] Persisted IV30 to history for %s", snap.AsOf.Format("2006-01-02"))
		}
	}

	// Output formatted JSON for user verification
	log.Println("=== RegimeState JSON Contract ===")
	rsBytes, _ := json.MarshalIndent(regimeState, "", "  ")
	fmt.Println(string(rsBytes))

	if len(regimeState.Decision.Rationale) > 0 {
		log.Println("Rationale:")
		for _, r := range regimeState.Decision.Rationale {
			log.Printf(" - %s", r)
		}
	}

	// 7. Intelligence Engine
	// log.Println("=== Option Chain Intelligence ===")
	// intelEngine := intel.NewEngine()
	// intelSnap := intelEngine.ComputeIntel(chainSnap, &surfaceSnap)

	// log.Println("--- Signals ---")
	// for _, sig := range intelSnap.Signals {
	// 	log.Printf(" SIGNAL [%s] (Score: %.2f) - %s", sig.Name, sig.Score, sig.Explanation)
	// }

	// log.Println("--- Key Metrics ---")
	// for _, exp := range intelSnap.Explanation {
	// 	log.Printf(" %s", exp)
	// }

	// // Output Intel JSON
	// log.Println("=== ChainIntelSnapshot JSON ===")
	// intelBytes, _ := json.MarshalIndent(intelSnap, "", "  ")
	// fmt.Println(string(intelBytes))

	// // 8. Strategy Selector
	// log.Println("=== Strategy Selector ===")
	// selector := strategy.NewSelector()
	// candidates, err := selector.SelectStrategies(&regimeState, &surfaceSnap, intelSnap, chainSnap, fwdState)
	// if err != nil {
	// 	log.Printf("Strategy selection error: %v", err)
	// } else {
	// 	log.Printf("Generated %d Candidates", len(candidates))
	// 	for _, cand := range candidates {
	// 		log.Printf(" > [%s] %s (%s) Score=%.2f", cand.StrategyType, cand.ID, cand.Intent, cand.Quality.Score)
	// 		log.Printf("   Entry: %s %.2f. Rationale: %v", cand.Entry.PremiumType, cand.Entry.NetPremium, cand.Rationale.Reasons)
	// 		for _, leg := range cand.Legs {
	// 			log.Printf("     - %4s %d %s %.0f @ %.2f", leg.Side, leg.Qty, leg.Type, leg.Strike, leg.Mark)
	// 		}
	// 	}

	// 	// Dump Candidates JSON
	// 	log.Println("=== StrategyCandidates JSON ===")
	// 	stratBytes, _ := json.MarshalIndent(candidates, "", "  ")
	// 	fmt.Println(string(stratBytes))
	// }

	log.Println("Pipeline finished.")
}
