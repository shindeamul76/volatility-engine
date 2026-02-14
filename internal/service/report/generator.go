package report

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"volatility-engine/internal/config"
	"volatility-engine/internal/domain/market"
	"volatility-engine/internal/domain/risk"
	"volatility-engine/internal/engines/payoff"
	reportEngine "volatility-engine/internal/engines/report"
	riskEngine "volatility-engine/internal/engines/risk"
	simEngine "volatility-engine/internal/engines/sim"
)

type Generator struct {
	simEvaluator   *simEngine.Evaluator
	payoffBuilder  *payoff.Builder
	riskAggregator *riskEngine.Aggregator
	jsonRenderer   *reportEngine.JSONRenderer
	mdRenderer     *reportEngine.MarkdownRenderer
	cfg            config.Config
}

func NewGenerator(cfg config.Config) *Generator {
	return &Generator{
		simEvaluator:   simEngine.NewEvaluator(), // Has internal cache
		payoffBuilder:  &payoff.Builder{},
		riskAggregator: &riskEngine.Aggregator{},
		jsonRenderer:   &reportEngine.JSONRenderer{},
		mdRenderer:     &reportEngine.MarkdownRenderer{},
		cfg:            cfg,
	}
}

// GenerateBatch processes multiple candidates in parallel.
func (g *Generator) GenerateBatch(
	snapshotID string,
	candidates []market.StrategyCandidate,
	snapshots map[string]market.Snapshot, // Keyed by underlying? Or just main snapshot?
	// Actually we act on one underlying usually.
	// But we need the Expiry Context (Skew) for each candidate.
	// We might need to pass a lookup for Skews/Contexts.
	// Let's pass the RegimeState which has IV30/Rank, and maybe a map of per-expiry data?
	regimeMeta struct {
		Regime string
		IV30   float64
		IVRank float64
	},
	// Helper to get Skew/Context for an expiry
	// For now, let's assume Simulator behaves autonomously regarding Skew (using basic rules or passed params)
	// But our Plan said "One SimContext per expiry".
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
				g.processCandidate(cand, baseDir, regimeMeta, riskFreeRate)
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
	regimeMeta struct {
		Regime string
		IV30   float64
		IVRank float64
	},
	r float64,
) {
	// Reconstruct Snapshot-like object for Evaluator?
	// Evaluator needs `market.Snapshot`.
	// We didn't pass full snapshot to batch (too big?).
	// We need Spot.
	// `config.Config` has Spot.

	spot := g.cfg.Market.Spot

	// Create minimal snapshot for Evaluator
	snap := market.Snapshot{
		AsOf: candidate.AsOf,
		Underlying: market.Underlying{
			Symbol: candidate.Underlying,
			Spot:   spot,
		},
	}

	// 1. Payoff
	payoffCurve := g.payoffBuilder.BuildPayoff(candidate, spot)
	payoffMetrics := g.payoffBuilder.CalculateMetrics(payoffCurve)

	// 2. Sim
	gridSpec := risk.ScenarioGridSpec{
		SpotShocks:     g.cfg.Risk.Scenario.SpotShocks,
		VolShocks:      g.cfg.Risk.Scenario.VolShocksAbs,
		TimeSteps:      g.cfg.Risk.Scenario.TimeShiftsDays,
		ScenariosCount: 0, // Calculated by len * len * len
	}
	// Re-calc scenarios count for info
	gridSpec.ScenariosCount = len(gridSpec.SpotShocks) * len(gridSpec.VolShocks) * len(gridSpec.TimeSteps)

	surface := g.simEvaluator.Evaluate(candidate, gridSpec, snap, r)

	// 3. Aggregate
	// Need current greeks
	currentGreeks := market.Greeks{}
	for _, res := range surface.Results {
		if res.Scenario.SpotChangePct == 0 && res.Scenario.VolChange == 0 && res.Scenario.DaysForward == 0 {
			currentGreeks = res.Greeks
			break
		}
	}

	report := g.riskAggregator.Summarize(candidate, surface, payoffMetrics, currentGreeks, regimeMeta)
	report.Meta.Spot = spot // Fill spot explicitly

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
