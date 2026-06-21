package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"volatility-engine/internal/app"
	"volatility-engine/internal/config"
	"volatility-engine/internal/replay"
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

	if len(os.Args) > 1 {
		cmd := os.Args[1]
		switch cmd {
		case "snapshot-run":
			runSnapshotPipeline()
		case "replay-run":
			runReplay()
		case "live-run":
			runLive()
		default:
			log.Fatalf("Unknown command: %s", cmd)
		}
	} else {
		fmt.Println("Usage: volengine <command>")
		fmt.Println("Commands:")
		fmt.Println("  snapshot-run    Run the pipeline on a single snapshot")
		fmt.Println("  replay-run      Run the historical replay loop")
		fmt.Println("  live-run        Fetch live NSE data every N minutes")
	}
}

func runSnapshotPipeline() {
	log.Println("Running snapshot pipeline...")

	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	engine := app.NewEngine(cfg)

	// Build SnapshotInput from config.yaml
	var files []app.FileSpec
	for _, f := range cfg.Files {
		expiry, err := cfg.ParseFileExpiry(f.Expiry)
		if err != nil {
			log.Fatalf("Failed to parse expiry for %s: %v", f.Path, err)
		}
		files = append(files, app.FileSpec{Path: f.Path, Expiry: expiry.UTC()})
	}

	in := app.SnapshotInput{
		AsOf:         time.Now().UTC(),
		Underlying:   cfg.Market.Underlying,
		Spot:         cfg.Market.Spot,
		RiskFreeRate: cfg.Pricing.RiskFreeRate,
		Files:        files,
	}

	out, err := engine.RunSnapshot(in)
	if err != nil {
		log.Fatalf("Engine failed: %v", err)
	}

	// Restore detailed logs
	app.LogSnapshotSummary(out.Snapshot)

	log.Printf("Regime: %s | IV30: %.2f%% (Rank: %.2f)",
		out.Regime.Decision.Regime, out.Regime.IVReference.IV*100, out.Regime.HistoricalContext.IVRank)

	app.LogCandidates(out.Candidates, &out.Surface, cfg.Pricing.RiskFreeRate)

	// Generate reports
	err = app.GenerateRiskReports(cfg, out.Snapshot.ID, out.Candidates, &out.Surface,
		out.Regime.Decision.Regime, out.Regime.IVReference.IV, out.Regime.HistoricalContext.IVRank,
		cfg.Pricing.RiskFreeRate)
	if err != nil {
		log.Printf("Risk report error: %v", err)
	}
} 

func runReplay() {
	log.Println("Running replay loop...")

	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	engine := app.NewEngine(cfg)

	// In a real app, pass this via flag. For now, default to testdata/snapshots
	baseDir := "testdata/snapshots"
	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		log.Printf("Warning: %s does not exist. trying 'data/snapshots'", baseDir)
		baseDir = "data/snapshots"
	}

	runID := time.Now().Format("20060102_150405")
	outDir := fmt.Sprintf("runs/%s", runID)
	if err := os.MkdirAll(outDir, 0777); err != nil {
		log.Fatalf("Failed to create run output dir: %v", err)
	}

	auditPath := filepath.Join(outDir, "events.jsonl")
	audit, err := replay.NewAuditor(auditPath)
	if err != nil {
		log.Fatalf("audit init failed: %v", err)
	}
	defer audit.Close()

	src := replay.NewSourceFS(baseDir)
	pf := replay.NewPortfolio(100000.0, cfg.Risk.ContractMultiplier) // Start with 100k cash, multiplier from config

	// Execution with config
	exec := replay.NewExecSim(
		replay.SlippageModel{
			Mode:     cfg.Execution.Slippage.Mode,
			Bps:      cfg.Execution.Slippage.Bps,
			Tick:     cfg.Execution.Slippage.Ticks,
			TickSize: cfg.Execution.TickSize,
		},
		replay.FeeModel{
			PerLeg:   cfg.Execution.Fees.PerLeg,
			PerOrder: cfg.Execution.Fees.PerOrder,
			Bps:      cfg.Execution.Fees.Bps,
		},
	)
	dec := replay.NewDecider(cfg.Replay)
	rg := replay.NewRiskGate(cfg.RiskGate)

	r := &replay.Runner{
		Cfg:        cfg,
		Engine:     engine,
		Source:     src,
		Decider:    dec,
		RiskGate:   rg,
		Exec:       exec,
		Audit:      audit,
		Alerts:     replay.NewAlertService(true),
		Pf:         pf,
		OutputDir:  outDir,
		PeakEquity: pf.InitialCash,
	}

	log.Printf("Reading snapshots from: %s", baseDir)
	log.Printf("Output run ID: %s", runID)

	if err := r.Run(baseDir); err != nil {
		log.Fatalf("replay failed: %v", err)
	}

	log.Printf("Replay finished. Audit log: %s", auditPath)
}

func runLive() {
	log.Println("Running live NSE fetch loop...")

	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if !cfg.Live.Enabled {
		log.Fatalf("Live mode is disabled in config. Set live.enabled: true")
	}

	engine := app.NewEngine(cfg)

	// Parse expiry specs from config
	expirySpecs, err := replay.ParseExpirySpecs(cfg.Live.Expiries)

	fmt.Println(expirySpecs)
	if err != nil {
		log.Fatalf("Failed to parse expiries: %v", err)
	}

	log.Printf("[LIVE] Symbol: %s, Expiries: %v, Interval: %d min",
		cfg.Live.Symbol, cfg.Live.Expiries, cfg.Live.IntervalMins)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	consecutiveFailures := 0
	maxConsecutiveFailures := 3

	// Create a SINGLE output directory for the entire live day session
	runID := time.Now().Format("20060102_150405")
	outDir := fmt.Sprintf("runs/live_session_%s", runID)
	if err := os.MkdirAll(outDir, 0777); err != nil {
		log.Fatalf("failed to create output dir: %v", err)
	}

	auditPath := filepath.Join(outDir, "events.jsonl")
	audit, err := replay.NewAuditor(auditPath)
	
	if err != nil {
		log.Fatalf("audit init failed: %v", err)
	}
	defer audit.Close()

	// Persist the portfolio and peak equity across all live cycles!
	pf := replay.NewPortfolio(100000.0, cfg.Risk.ContractMultiplier)
	peakEquity := pf.InitialCash
	var globalEquityCurve []replay.EquityPoint

	// runOneCycle performs a single live fetch + engine run
	runOneCycle := func() error {
		tempDir, err := os.MkdirTemp("", "nse-live-*")
		if err != nil {
			return fmt.Errorf("failed to create temp dir: %w", err)
		}
		defer os.RemoveAll(tempDir)

		src := replay.NewSourceNSE(cfg.Live.Symbol, expirySpecs, cfg.Pricing.RiskFreeRate, tempDir)

		exec := replay.NewExecSim(
			replay.SlippageModel{
				Mode:     cfg.Execution.Slippage.Mode,
				Bps:      cfg.Execution.Slippage.Bps,
				Tick:     cfg.Execution.Slippage.Ticks,
				TickSize: cfg.Execution.TickSize,
			},
			replay.FeeModel{
				PerLeg:   cfg.Execution.Fees.PerLeg,
				PerOrder: cfg.Execution.Fees.PerOrder,
				Bps:      cfg.Execution.Fees.Bps,
			},
		)
		dec := replay.NewDecider(cfg.Replay)
		rg := replay.NewRiskGate(cfg.RiskGate)

		r := &replay.Runner{
			Cfg:        cfg,
			Engine:     engine,
			Source:     src,
			Decider:    dec,
			RiskGate:   rg,
			Exec:       exec,
			Audit:      audit,
			Alerts:     replay.NewAlertService(true),
			Pf:          pf,
			OutputDir:   outDir,
			PeakEquity:  peakEquity,
			EquityCurve: globalEquityCurve,
		}

		log.Printf("[LIVE] Starting cycle %s", time.Now().Format("15:04:05"))
		if err := r.Run(tempDir); err != nil {
			return fmt.Errorf("live cycle failed: %w", err)
		}
		peakEquity = r.PeakEquity
		globalEquityCurve = r.EquityCurve

		log.Printf("[LIVE] Cycle complete. Output: %s", outDir)
		return nil
	}

	// Run immediately on startup
	log.Println("[LIVE] Running initial fetch...")
	if err := runOneCycle(); err != nil {
		log.Printf("[LIVE] Initial fetch failed: %v", err)
		consecutiveFailures++
	} else {
		consecutiveFailures = 0
	}

	// Start ticker
	interval := time.Duration(cfg.Live.IntervalMins) * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("[LIVE] Ticker started: every %d minutes. Press Ctrl+C to stop.", cfg.Live.IntervalMins)

	for {
		select {
		case <-sigCh:
			log.Println("[LIVE] Shutdown signal received. Exiting gracefully.")
			return
		case t := <-ticker.C:
			log.Printf("[LIVE] Tick at %s", t.Format(time.RFC3339))

			if consecutiveFailures >= maxConsecutiveFailures {
				log.Printf("[LIVE] %d consecutive failures. Falling back to SourceFS from %s",
					consecutiveFailures, cfg.Live.FallbackDir)

				fsSrc := replay.NewSourceFS(cfg.Live.FallbackDir)
				exec := replay.NewExecSim(
					replay.SlippageModel{
						Mode:     cfg.Execution.Slippage.Mode,
						Bps:      cfg.Execution.Slippage.Bps,
						Tick:     cfg.Execution.Slippage.Ticks,
						TickSize: cfg.Execution.TickSize,
					},
					replay.FeeModel{
						PerLeg:   cfg.Execution.Fees.PerLeg,
						PerOrder: cfg.Execution.Fees.PerOrder,
						Bps:      cfg.Execution.Fees.Bps,
					},
				)
				dec := replay.NewDecider(cfg.Replay)
				rg := replay.NewRiskGate(cfg.RiskGate)

				r := &replay.Runner{
					Cfg:        cfg,
					Engine:     engine,
					Source:     fsSrc,
					Decider:    dec,
					RiskGate:   rg,
					Exec:       exec,
					Audit:      audit,
					Alerts:      replay.NewAlertService(true),
					Pf:          pf,
					OutputDir:   outDir,
					PeakEquity:  peakEquity,
					EquityCurve: globalEquityCurve,
				}

				if err := r.Run(cfg.Live.FallbackDir); err != nil {
					log.Printf("[LIVE] Fallback run failed: %v", err)
				} else {
					log.Printf("[LIVE] Fallback run complete.")
				}
				peakEquity = r.PeakEquity
				globalEquityCurve = r.EquityCurve

				// Reset and try live again next tick
				consecutiveFailures = 0
				continue
			}

			if err := runOneCycle(); err != nil {
				log.Printf("[LIVE] Cycle failed: %v", err)
				consecutiveFailures++
				log.Printf("[LIVE] Consecutive failures: %d/%d", consecutiveFailures, maxConsecutiveFailures)
			} else {
				consecutiveFailures = 0
			}
		}
	}
}
