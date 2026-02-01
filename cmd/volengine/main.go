package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"volatility-engine/internal/chain"
	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/ingest"
	"volatility-engine/internal/quant/intel"
	"volatility-engine/internal/quant/iv"
	"volatility-engine/internal/quant/pricing"
	"volatility-engine/internal/quant/regime"
	"volatility-engine/internal/quant/strategy"
	"volatility-engine/internal/quant/surface"
)

func main() {
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
	log.Println("Running snapshot pipeline (prototype)...")

	// Hardcoded vars for prototype "walking skeleton"
	filePath := "testdata/snapshots/nifty-budget.csv"
	underlying := "NIFTY"
	spot := 24825.45 // NIFTY Spot Price

	ist, _ := time.LoadLocation("Asia/Kolkata")
	expiryIST := time.Date(2026, 2, 3, 15, 30, 0, 0, ist)
	expiry := expiryIST.UTC()

	// 1. Ingest
	ingestSvc := ingest.NewIngestService()

	// Fake S3 Key for prototype
	s3Key := fmt.Sprintf("s3://vol-engine/raw/prototypes/%s_%s.csv", underlying, time.Now().Format("20060102"))
	snap, err := ingestSvc.IngestSnapshot(filePath, underlying, spot, expiry, s3Key)

	if err != nil {
		log.Fatalf("Ingestion failed: %v", err)
	}

	log.Printf("Snapshot Ingested: %s", snap.ID)
	log.Printf("Underlying: %s, Spot: %.2f", snap.Underlying.Symbol, snap.Underlying.Spot)
	log.Printf("Total Quotes: %d", len(snap.Quotes))
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

	// 2. Chain Generation
	log.Println("Generating Chain Snapshot...")
	chainProc := chain.NewProcessor(0.06) // 6% Risk Free Rate
	chainSnap, err := chainProc.GenerateChain(snap, expiry)
	if err != nil {
		log.Fatalf("Chain generation failed: %v", err)
	}

	// log.Printf("Chain Generated: %s (ATM: %.2f)", chainSnap.ID, chainSnap.ChainState.ATMStrike)

	// Dump Chain JSON
	// chainBytes, _ := json.MarshalIndent(chainSnap, "", "  ")
	// fmt.Println(string(chainBytes))

	// // 3. Pricing Example (ATM Call)
	log.Println("Calculating Pricing for ATM Strike...")

	// // Use Implied Forward if available
	S_input := chainSnap.Underlying.Spot
	isForward := false

	fmt.Println("ImpliedForward: ", chainSnap.ChainState.ImpliedForward)
	if chainSnap.ChainState.ImpliedForward > 0 {
		S_input = chainSnap.ChainState.ImpliedForward
		isForward = true
		log.Printf("Using Implied Forward: %.2f", S_input)
	}

	fmt.Println("IsForward: ", isForward)

	atmStrike := chainSnap.ChainState.ATMStrike
	kStr := fmt.Sprintf("%.0f", atmStrike)
	atmChain := chainSnap.ByStrike[kStr]

	fmt.Println("ATMCHAIN: ", atmChain)
	fmt.Println("ATMCHAIN.Call: ", atmChain.Call)
	fmt.Println("ATMCHAIN.Put: ", atmChain.Put)

	// Calculate TTE and DF (needed for pricing and IV solving)
	tte := chainSnap.Expiry.TTEYears
	if tte == 0 {
		tte = float64(chainSnap.Expiry.DaysToExpiry) / 365.0
	}
	df := math.Exp(-0.06 * tte)
	fmt.Println("Discount: ", df)

	var fwdState *market.ForwardState

	if atmChain != nil && atmChain.Call != nil {
		pCtx := pricing.PricingContext{
			Type:           market.Call,
			S:              S_input,
			K:              atmStrike,
			T:              tte,
			R:              0.06, // 6% Risk Free Rate
			Q:              0.0,
			Sigma:          0.2260, // 22.60% Vol
			IsForwardModel: isForward,
		}

		res := pricing.Calculate(pCtx)

		// 	// Build PricingResult JSON
		pricingResult := market.PricingResult{
			RequestID: fmt.Sprintf("prc_req_%s", time.Now().Format("150405")),
			ModelUsed: "BLACK_SCHOLES_FORWARD",
			InputsEcho: market.PricingInputs{
				Forward:        S_input,
				Strike:         atmStrike,
				TTEYears:       tte,
				RiskFreeRateCC: 0.06,
				DiscountFactor: df,
				Volatility:     0.2260,
				Type:           "CALL",
			},
			Outputs: market.PricingOutputs{
				TheoreticalPrice: res.Price,
				Greeks: market.GreeksOutput{
					Delta:           res.Greeks.Delta,
					Gamma:           res.Greeks.Gamma,
					ThetaPerDay:     res.Greeks.ThetaPerDay,
					VegaPerVolPoint: res.Greeks.VegaPerVolPoint,
				},
				Intermediates: market.PricingIntermediates{
					D1:  res.Intermediates.D1,
					D2:  res.Intermediates.D2,
					Nd1: res.Intermediates.Nd1,
					Nd2: res.Intermediates.Nd2,
				},
			},
			Quality: market.PricingQuality{
				OK:       res.Quality.OK,
				Flags:    res.Quality.Flags,
				Warnings: res.Quality.Warnings,
			},
		}

		// 	// Generate ForwardState
		fwdState = chainProc.GenerateForwardState(snap, chainSnap, expiry)

		// 	// Output JSON
		log.Println("=== ForwardState JSON ===")
		// fwdBytes, _ := json.MarshalIndent(fwdState, "", "  ")
		// fmt.Println(string(fwdBytes))

		log.Println("=== PricingResult JSON ===")
		prcBytes, _ := json.MarshalIndent(pricingResult, "", "  ")
		fmt.Println(string(prcBytes))
	} else {
		// Just creating empty fwdState for scope availability, ideally should always calculate
		log.Println("Warning: ATM Chain not found, skipping specific Pricing calc but generating ForwardState anyway")
		fwdState = chainProc.GenerateForwardState(snap, chainSnap, expiry)
	}

	// // 4. IV Solving for all eligible quotes
	log.Println("=== IV Solving ===")
	ivSolver := iv.NewSolver(iv.DefaultSettings())
	ivResults := []market.IVResult{}

	for _, ivInput := range chainSnap.DownstreamReadySets.IVSurfaceInputs {
		// Find the quote data
		kStr := fmt.Sprintf("%.0f", ivInput.Strike)
		sc := chainSnap.ByStrike[kStr]
		if sc == nil {
			continue
		}

		var opt *market.Option
		if ivInput.Type == market.Call {
			opt = sc.Call
		} else {
			opt = sc.Put
		}
		if opt == nil || opt.Mid <= 0 {
			continue
		}

		// 	// Build IVRequest
		ivReq := market.IVRequest{
			ID:   fmt.Sprintf("iv_%s_%.0f_%s", expiry.Format("20060102"), ivInput.Strike, ivInput.Type),
			AsOf: snap.AsOf,
			Instrument: market.IVInstrument{
				Underlying: snap.Underlying.Symbol,
				Expiry:     expiry.Format("2006-01-02"),
				Strike:     ivInput.Strike,
				Type:       ivInput.Type,
			},
			Market: market.IVMarket{
				MarkPrice:  opt.Mid,
				MarkSource: "MID",
				Bid:        opt.Bid,
				Ask:        opt.Ask,
				SpreadPct:  (opt.Ask - opt.Bid) / opt.Mid,
			},
			Context: market.IVContext{
				Spot:          snap.Underlying.Spot,
				Forward:       S_input,
				ForwardSource: "PUT_CALL_PARITY",
				TTEYears:      tte,
			},
			Rates: market.Rates{
				RiskFreeRateCC: 0.06,
				DiscountFactor: df,
			},
			Settings: iv.DefaultSettings(),
		}

		ivRes := ivSolver.Solve(ivReq)
		ivResults = append(ivResults, ivRes)

		if ivRes.Status == market.IVStatusConverged {
			log.Printf("  %.0f %s: IV=%.2f%% (conf=%.2f)",
				ivInput.Strike, ivInput.Type, ivRes.Result.ImpliedVol*100, ivRes.Quality.Confidence)
		}
	}

	// Output first few IV results as JSON
	log.Println("=== Sample IVResults JSON ===")
	sampleCount := 10

	fmt.Println("Total IV Results: ", len(ivResults))
	if len(ivResults) < sampleCount {
		sampleCount = len(ivResults)
	}
	for i := 0; i < sampleCount; i++ {
		ivBytes, _ := json.MarshalIndent(ivResults[i], "", "  ")
		fmt.Println(string(ivBytes))
	}

	// 5. Build Volatility Surface
	log.Println("=== Building IV Surface ===")

	// Convert solver results to surface inputs (IVPoint)
	// We need to map back to the request details.
	// For this prototype, we'll reconstruct the IVPoint from the IVResult + Context we used.
	// Ideally, IVResult should link back to Request, but we can do a localized loop since we have the inputs.
	// Actually, best way in this "main" script is to just build the points list AS we solve.

	// Re-iterating to demonstrate clear separation:
	var surfacePoints []market.IVPoint

	// We need to re-loop or store the inputs.
	// Let's assume we can map the `ivResults` back.
	// IVResult has `RequestID`. We constructed IDs as `iv_YYYYMMDD_STRIKE_TYPE`.
	// Parsing is brittle.
	// BETTER approach for this script: Build `surfacePoints` inside the solving loop above.

	// Let's rebuild the loop above to collect points.
	// SINCE `replace_file_content` replaces a block, I will just recreate the points from the `chainSnap` + `ivResults` assuming order matches?
	// NO, order is not guaranteed if we were async (we are sync here).
	// To be safe, I'll modify the previous loop in a separate edit or just "hack" it here by re-traversing `chainSnap`
	// and looking up the successful results? No, `ivResults` is a list of results.

	// Okay, I will modify the loop structure in `main.go` to capture `surfacePoints` alongside `ivResults`.
	// But `replace_file_content` works on contiguous blocks.
	// I will act on the end of the file here, but I really need to modify the loop roughly lines 179-234.
	// I will abort this specific tool call and do a larger multi_replace or helper mechanism.
	// Wait, I can just iterate `ivResults`? IT DOES NOT HAVE STRIKE/TYPE info in `IVResultData`.
	// `IVResult` has `RequestID`.
	// `RequestID` format: `iv_20260205_23300_PUT`.
	// I can parse this string.

	surfacePoints = make([]market.IVPoint, 0, len(ivResults))
	for _, res := range ivResults {
		if res.Status != market.IVStatusConverged {
			continue
		}

		// Parse ID: "iv_DATE_STRIKE_TYPE"
		// Using Split since Sscanf %s consumes underscores
		parts := strings.Split(res.RequestID, "_")
		if len(parts) < 4 {
			continue
		}
		// parts[0] = "iv"
		// parts[1] = dateStr
		// parts[2] = strikeStr
		// parts[3] = typeStr

		typeStr := parts[3]
		strike, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			continue
		}

		// We also need Forward and Expiry Date.
		// Expiry is `dateStr` (formatted YYYYMMDD).
		// Forward? usage in LogMoneyness requires F.
		// `IVResult` doesn't have it.
		// `IVPoint` needs to be passed to builder.
		// `Builder.BuildFromPoints` calculates LogMoneyness. It needs F.
		// `Builder` expects `BuildSkew` to calculate it using passed F.
		// `BuildFromPoints` calls `BuildSkew(expiry, points, 0, 0)` -> Passing 0 Forward!
		// My `Builder.BuildFromPoints` implementation had a TODO about Forward.
		// I recall stubbing it with 0.
		// I must fix `Builder.BuildFromPoints` to accept a map of Forwards, OR pass Forward in IVPoint.
		// `IVPoint` has `LogMoneyness`, but `Builder` re-calculates it.
		// Let's check `builder.go`:
		// `chosen.LogMoneyness = math.Log(k / forward)`
		// It uses the `forward` arg passed to `BuildSkew`.
		// And `BuildFromPoints` passes 0.
		// THIS IS A BUG in my previous step's Builder logic if I rely on `BuildFromPoints` without arguments.

		// Correction: I should update `BuildFromPoints` to take a `ForwardMap` or `ChainSnapshot`.
		// OR, since this is `main.go`, I can call `builder.BuildSkew` directly for each expiry since I know F.

		// Let's do that. Direct orchestration in main is safer for prototype.

		p := market.IVPoint{
			Expiry:     expiry.Format("2006-01-02"), // Derived from context knowing main loop is single expiry
			Strike:     strike,
			Type:       market.OptionType(typeStr),
			ImpliedVol: res.Result.ImpliedVol,
			Confidence: res.Quality.Confidence,
			MarkSource: "MID", // Hardcoded in main loop
			// MarkPrice? We don't have it in Result.
		}
		surfacePoints = append(surfacePoints, p)
	}

	// Direct BuildSkew call since we have single expiry context `chainSnap`
	surfaceBuilder := surface.NewBuilder(surface.DefaultSettings())

	// Need Forward. `S_input` was used for pricing/solving.
	// `expiry` formatting same as used in loop.

	skewSnap := surfaceBuilder.BuildSkew(expiry.Format("2006-01-02"), surfacePoints, S_input, tte)

	log.Printf("Surface Built for %s:", skewSnap.Expiry)
	log.Printf("  ATM Vol: %.2f%%", skewSnap.Metrics.ATMVol*100)
	log.Printf("  Skew Slope: %.4f", skewSnap.Metrics.SkewSlope)
	log.Printf("  Curvature: %.4f", skewSnap.Metrics.Curvature)
	log.Printf("  Points Used: %d / %d", skewSnap.Fit.Quality.PointsUsed, len(skewSnap.Points))

	// Sanity Checks
	minX, maxX := 1000.0, -1000.0
	calls, puts := 0, 0

	log.Println("--- Sanity Check: X-Axis & OTM Selection ---")
	for i, p := range skewSnap.Points {
		if p.LogMoneyness < minX {
			minX = p.LogMoneyness
		}
		if p.LogMoneyness > maxX {
			maxX = p.LogMoneyness
		}

		if p.Type == market.Call {
			calls++
		} else {
			puts++
		}

		// Print a few samples across the range
		if i == 0 || i == len(skewSnap.Points)/2 || i == len(skewSnap.Points)-1 {
			log.Printf("  Sample [%d]: Strike=%.0f Type=%s IV=%.2f%% X=%.4f Conf=%.2f W=%.4f",
				i, p.Strike, p.Type, p.ImpliedVol*100, p.LogMoneyness, p.Confidence, p.Weight)
		}
	}
	log.Printf("  X-Axis Range: [%.4f, %.4f]", minX, maxX)
	log.Printf("  Composition: %d Calls, %d Puts", calls, puts)

	// 6. Regime Detection
	log.Println("=== Regime Detection ===")

	// Create a surface snapshot wrapper
	surfaceSnap := market.IVSurfaceSnapshot{
		AsOf:       snap.AsOf,
		Underlying: snap.Underlying.Symbol,
		Skews:      []market.IVSkewSnapshot{skewSnap},
	}

	detector := regime.NewDetector(regime.DefaultSettings())
	regimeState := detector.Detect(surfaceSnap)

	log.Printf("Regime: %s (Bias: %s)", regimeState.Decision.Regime, regimeState.Decision.Bias)
	log.Printf("Score: %.2f", regimeState.Decision.Score)
	log.Printf("Reference IV (30d): %.2f%% (Conf: %.2f)", regimeState.IVReference.IV*100, regimeState.IVReference.Confidence)
	log.Printf("Historical Context: Rank=%.2f, Pct=%.2f", regimeState.HistoricalContext.IVRank, regimeState.HistoricalContext.IVPercentile)
	log.Printf("Realized Vol (HV20): %.2f%% (Ratio: %.2f)", regimeState.RealizedVol.HV*100, regimeState.RealizedVol.IVHVRatio)

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
	log.Println("=== Option Chain Intelligence ===")
	intelEngine := intel.NewEngine()
	intelSnap := intelEngine.ComputeIntel(chainSnap, &surfaceSnap)

	log.Println("--- Signals ---")
	for _, sig := range intelSnap.Signals {
		log.Printf(" SIGNAL [%s] (Score: %.2f) - %s", sig.Name, sig.Score, sig.Explanation)
	}

	log.Println("--- Key Metrics ---")
	for _, exp := range intelSnap.Explanation {
		log.Printf(" %s", exp)
	}

	// Output Intel JSON
	log.Println("=== ChainIntelSnapshot JSON ===")
	intelBytes, _ := json.MarshalIndent(intelSnap, "", "  ")
	fmt.Println(string(intelBytes))

	// 8. Strategy Selector
	log.Println("=== Strategy Selector ===")
	selector := strategy.NewSelector()
	candidates, err := selector.SelectStrategies(&regimeState, &surfaceSnap, intelSnap, chainSnap, fwdState)
	if err != nil {
		log.Printf("Strategy selection error: %v", err)
	} else {
		log.Printf("Generated %d Candidates", len(candidates))
		for _, cand := range candidates {
			log.Printf(" > [%s] %s (%s) Score=%.2f", cand.StrategyType, cand.ID, cand.Intent, cand.Quality.Score)
			log.Printf("   Entry: %s %.2f. Rationale: %v", cand.Entry.PremiumType, cand.Entry.NetPremium, cand.Rationale.Reasons)
			for _, leg := range cand.Legs {
				log.Printf("     - %4s %d %s %.0f @ %.2f", leg.Side, leg.Qty, leg.Type, leg.Strike, leg.Mark)
			}
		}

		// Dump Candidates JSON
		log.Println("=== StrategyCandidates JSON ===")
		stratBytes, _ := json.MarshalIndent(candidates, "", "  ")
		fmt.Println(string(stratBytes))
	}

	log.Println("Pipeline finished.")
}
