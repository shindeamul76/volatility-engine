package report

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"volatility-engine/internal/config"
	"volatility-engine/internal/domain/market"
	reportEngine "volatility-engine/internal/engines/report"
	"volatility-engine/internal/quant/risk"
)

type Generator struct {
	jsonRenderer *reportEngine.JSONRenderer
	mdRenderer   *reportEngine.MarkdownRenderer
	cfg          config.Config
}

func NewGenerator(cfg config.Config) *Generator {
	return &Generator{
		jsonRenderer: &reportEngine.JSONRenderer{},
		mdRenderer:   &reportEngine.MarkdownRenderer{},
		cfg:          cfg,
	}
}

// GenerateBatch processes multiple candidates in parallel.
func (g *Generator) GenerateBatch(
	snapshotID string,
	candidates []market.StrategyCandidate,
	surface *market.IVSurfaceSnapshot,
	regimeMeta struct {
		Regime string
		IV30   float64
		IVRank float64
	},
	riskFreeRate float64,
) error {

	// Workers
	tasks := make(chan market.StrategyCandidate, len(candidates))
	var wg sync.WaitGroup

	workerCount := g.cfg.Report.Workers
	if workerCount <= 0 {
		workerCount = 1
	}

	// Ensure output dir exists
	baseDir := filepath.Join(g.cfg.Report.OutDir, snapshotID)
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("failed to create report dir: %w", err)
	}

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for cand := range tasks {
				g.processCandidate(cand, baseDir, surface, regimeMeta, riskFreeRate)
			}
		}()
	}

	for _, c := range candidates {
		tasks <- c
	}
	close(tasks)
	wg.Wait()

	return nil
}

func (g *Generator) processCandidate(
	candidate market.StrategyCandidate,
	outDir string,
	surface *market.IVSurfaceSnapshot,
	regimeMeta struct {
		Regime string
		IV30   float64
		IVRank float64
	},
	r float64,
) {
	spot := g.cfg.Market.Spot

	// 1. Payoff Analysis
	payoffMetrics := risk.AnalyzePayoff(candidate.Legs)

	// 2. Scenario Analysis
	// Prepare Skew Map
	skews := make(map[string]market.IVSkewSnapshot)
	if surface != nil {
		for _, s := range surface.Skews {
			skews[s.Expiry] = s
		}
	}

	gridSpec := risk.ScenarioGridSpec{
		SpotShocks:   g.cfg.Risk.Scenario.SpotShocks,
		VolShocksAbs: g.cfg.Risk.Scenario.VolShocksAbs,
		TimeSteps:    g.cfg.Risk.Scenario.TimeShiftsDays,
	}
	// Defaults if config missing
	if len(gridSpec.SpotShocks) == 0 {
		gridSpec = risk.DefaultScenarioGrid()
	}

	scenarios := risk.RunScenarios(candidate, spot, r, skews, gridSpec)

	// 3. Construct Report
	report := risk.RiskReport{
		Meta: risk.ReportMeta{
			AsOf:       candidate.AsOf,
			Underlying: candidate.Underlying,
			Spot:       spot,
			Expiry:     candidate.Expiry,
			Regime:     regimeMeta.Regime,
			IV30:       regimeMeta.IV30,
			IVRank:     regimeMeta.IVRank,
		},
		Candidate: candidate,
		Payoff:    payoffMetrics,
		Greeks:    scenarios.BaseCase.Greeks,
		Scenarios: scenarios,
	}

	// 4. Render & Save
	// JSON
	if contains(g.cfg.Report.Formats, "json") {
		data, _ := g.jsonRenderer.Render(report)
		_ = os.WriteFile(filepath.Join(outDir, candidate.ID+".json"), data, 0644)
	}

	// MD
	if contains(g.cfg.Report.Formats, "markdown") {
		data, _ := g.mdRenderer.Render(report)
		_ = os.WriteFile(filepath.Join(outDir, candidate.ID+".md"), data, 0644)
	}
}

func contains(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}
