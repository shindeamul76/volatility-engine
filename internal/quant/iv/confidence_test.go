package iv

import (
	"math"
	"testing"

	"volatility-engine/internal/domain/market"
)

func TestConfidenceScoring(t *testing.T) {
	solver := NewSolver(DefaultSettings())

	// Helper to create a base mock result/request
	createMock := func() (market.IVRequest, market.IVResult) {
		req := market.IVRequest{
			Market: market.IVMarket{
				MarkSource:   "MID",
				SpreadPct:    0.033, // 3.3%
				Volume:       1500,  // High
				OpenInterest: 15000, // High
			},
		}
		res := market.IVResult{
			Status: market.IVStatusConverged,
			Result: market.IVResultData{
				Iterations: 7,
			},
			Diagnostics: market.IVDiagnostics{
				VegaAtSolution: 12.4,
			},
		}
		return req, res
	}

	t.Run("User Example Scenario", func(t *testing.T) {
		// Matches the user provided example:
		// Spread 3.3% -> Score 0.8
		// Volume High -> 1.0
		// OI High -> 1.0
		// Liquidity Score = sqrt(0.8 * 1.0 * 1.0) = 0.8944
		// Solver Iter 7 -> 1.0
		// Solver Vega 12.4 -> 1.0
		// Solver Score = 1.0 * 1.0 = 1.0
		// Mark MID -> 1.0
		// Consistency -> 0.8 (Default)
		//
		// Final = 0.45*0.8944 + 0.25*1.0 + 0.15*1.0 + 0.15*0.8
		//       = 0.40248 + 0.25 + 0.15 + 0.12 = 0.92248

		req, res := createMock()

		conf := solver.calculateConfidence(req, res)

		// Expected calculation:
		// Liq: sqrt(0.8 * 1 * 1) = 0.894427
		// Solver: 1.0
		// Mark: 1.0
		// Consistency: 0.8 (default)
		// Weighted: 0.45*0.894427 + 0.25*1.0 + 0.15*1.0 + 0.15*0.8
		//         = 0.402492 + 0.25 + 0.15 + 0.12
		//         = 0.922492

		expected := 0.9225
		if math.Abs(conf-expected) > 0.001 {
			t.Errorf("Confidence mismatch. Got %.4f, expected ~%.4f", conf, expected)
		}
	})

	t.Run("Wide Spread Penalty", func(t *testing.T) {
		req, res := createMock()
		req.Market.SpreadPct = 0.16 // > 15% -> Score 0.0

		conf := solver.calculateConfidence(req, res)

		// Liq: sqrt(0 * 1 * 1) = 0
		// Solver: 1.0
		// Mark: 1.0
		// Consistency: 0.8
		// Weighted: 0 + 0.25 + 0.15 + 0.12 = 0.52

		expected := 0.52
		if math.Abs(conf-expected) > 0.001 {
			t.Errorf("Confidence mismatch for wide spread. Got %.4f, expected ~%.4f", conf, expected)
		}
	})

	t.Run("Solver Unstable Penalty", func(t *testing.T) {
		req, res := createMock()
		res.Result.Iterations = 25           // Score 0.5
		res.Diagnostics.VegaAtSolution = 0.4 // < 0.5 -> Score 0.3

		// SolverScore = 0.5 * 0.3 = 0.15

		conf := solver.calculateConfidence(req, res)

		// Liq: 0.8944 (from before)
		// Solver: 0.15
		// Mark: 1.0
		// Consistency: 0.8
		// Weighted: 0.45*0.8944 + 0.25*0.15 + 0.15*1.0 + 0.15*0.8
		//         = 0.4025 + 0.0375 + 0.15 + 0.12
		//         = 0.71

		expected := 0.71
		if math.Abs(conf-expected) > 0.01 {
			t.Errorf("Confidence mismatch for unstable solver. Got %.4f, expected ~%.4f", conf, expected)
		}
	})

	t.Run("Not Converged", func(t *testing.T) {
		req, res := createMock()
		res.Status = market.IVStatusMaxIterations

		conf := solver.calculateConfidence(req, res)

		if conf != 0.0 {
			t.Errorf("Expected confidence 0.0 for non-converged, got %.4f", conf)
		}
	})

	t.Run("Market Source Penalty", func(t *testing.T) {
		req, res := createMock()
		req.Market.MarkSource = "LTP" // Score 0.6

		conf := solver.calculateConfidence(req, res)

		// Liq: 0.8944
		// Solver: 1.0
		// Mark: 0.6
		// Consistency: 0.8
		// Weighted: 0.4025 + 0.25 + 0.15*0.6 + 0.12
		//         = 0.4025 + 0.25 + 0.09 + 0.12
		//         = 0.8625

		expected := 0.8625
		if math.Abs(conf-expected) > 0.001 {
			t.Errorf("Confidence mismatch for LTP source. Got %.4f, expected ~%.4f", conf, expected)
		}
	})
}
