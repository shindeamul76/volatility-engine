package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"volatility-engine/internal/chain"
	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/ingest"
	"volatility-engine/internal/quant/iv"
	"volatility-engine/internal/quant/pricing"
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
	filePath := "testdata/snapshots/nifty-sample-1.csv"
	underlying := "NIFTY"
	spot := 25320.65 // NIFTY Spot Price
	expiry := time.Date(2026, 2, 5, 15, 30, 0, 0, time.UTC)

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
		log.Println("Sample Quote (first 3):")
		for i := 0; i < len(snap.Quotes) && i < 3; i++ {
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

	log.Printf("Chain Generated: %s (ATM: %.2f)", chainSnap.ID, chainSnap.ChainState.ATMStrike)

	// Dump Chain JSON
	// chainBytes, _ := json.MarshalIndent(chainSnap, "", "  ")
	// fmt.Println(string(chainBytes))

	// 3. Pricing Example (ATM Call)
	log.Println("Calculating Pricing for ATM Strike...")

	// Use Implied Forward if available
	S_input := chainSnap.Underlying.Spot
	isForward := false
	if chainSnap.ChainState.ImpliedForward > 0 {
		S_input = chainSnap.ChainState.ImpliedForward
		isForward = true
		log.Printf("Using Implied Forward: %.2f", S_input)
	}

	atmStrike := chainSnap.ChainState.ATMStrike
	kStr := fmt.Sprintf("%.0f", atmStrike)
	atmChain := chainSnap.ByStrike[kStr]

	// Calculate TTE and DF (needed for pricing and IV solving)
	tte := chainSnap.Expiry.TTEYears
	if tte == 0 {
		tte = float64(chainSnap.Expiry.DaysToExpiry) / 365.0
	}
	df := math.Exp(-0.06 * tte)

	if atmChain != nil && atmChain.Call != nil {
		pCtx := pricing.PricingContext{
			Type:           market.Call,
			S:              S_input,
			K:              atmStrike,
			T:              tte,
			R:              0.06, // 6% Risk Free Rate
			Q:              0.0,
			Sigma:          0.176, // 17.60% Vol
			IsForwardModel: isForward,
		}

		res := pricing.Calculate(pCtx)

		// Build PricingResult JSON
		pricingResult := market.PricingResult{
			RequestID: fmt.Sprintf("prc_req_%s", time.Now().Format("150405")),
			ModelUsed: "BLACK_SCHOLES_FORWARD",
			InputsEcho: market.PricingInputs{
				Forward:        S_input,
				Strike:         atmStrike,
				TTEYears:       tte,
				RiskFreeRateCC: 0.06,
				DiscountFactor: df,
				Volatility:     0.176,
				Type:           "CALL",
			},
			Outputs: market.PricingOutputs{
				TheoreticalPrice: res.Price,
				Greeks: market.GreeksOutput{
					Delta: res.Greeks.Delta,
					Gamma: res.Greeks.Gamma,
					Theta: res.Greeks.Theta,
					Vega:  res.Greeks.Vega,
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

		// Generate ForwardState
		fwdState := chainProc.GenerateForwardState(snap, chainSnap, expiry)

		// Output JSON
		log.Println("=== ForwardState JSON ===")
		fwdBytes, _ := json.MarshalIndent(fwdState, "", "  ")
		fmt.Println(string(fwdBytes))

		log.Println("=== PricingResult JSON ===")
		prcBytes, _ := json.MarshalIndent(pricingResult, "", "  ")
		fmt.Println(string(prcBytes))
	}

	// 4. IV Solving for all eligible quotes
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

		// Build IVRequest
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
	sampleCount := 3
	if len(ivResults) < sampleCount {
		sampleCount = len(ivResults)
	}
	for i := 0; i < sampleCount; i++ {
		ivBytes, _ := json.MarshalIndent(ivResults[i], "", "  ")
		fmt.Println(string(ivBytes))
	}

	log.Printf("IV Solved: %d quotes", len(ivResults))
	log.Println("Pipeline finished.")
}
