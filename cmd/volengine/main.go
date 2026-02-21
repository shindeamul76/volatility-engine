package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
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
		default:
			log.Fatalf("Unknown command: %s", cmd)
		}
	} else {
		fmt.Println("Usage: volengine <command>")
		fmt.Println("Commands:")
		fmt.Println("  snapshot-run    Run the pipeline on a single snapshot")
		fmt.Println("  replay-run      Run the historical replay loop")
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
