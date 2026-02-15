package replay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Metrics represents backtest performance metrics
type Metrics struct {
	TotalTrades    int     `json:"total_trades"`
	WinRate        float64 `json:"win_rate"`
	Wins           int     `json:"wins"`
	Losses         int     `json:"losses"`
	AvgWin         float64 `json:"avg_win"`
	AvgLoss        float64 `json:"avg_loss"`
	MaxDrawdown    float64 `json:"max_drawdown"`
	AvgHoldingDays float64 `json:"avg_holding_days"`
	RealizedPnL    float64 `json:"realized_pnl"`
	FinalEquity    float64 `json:"final_equity"`
	InitialCash    float64 `json:"initial_cash"`
	ReturnPct      float64 `json:"return_pct"`
}

// EquityPoint represents a point in the equity curve
type EquityPoint struct {
	Time     time.Time
	Equity   float64
	Realized float64
}

// ComputeMetrics calculates performance metrics from portfolio and equity curve
func ComputeMetrics(pf *Portfolio, equityCurve []EquityPoint) Metrics {
	m := Metrics{
		TotalTrades: pf.TotalTrades,
		Wins:        pf.Wins,
		Losses:      pf.Losses,
		RealizedPnL: pf.RealizedPnL,
		InitialCash: pf.InitialCash,
	}

	// Win rate
	if m.TotalTrades > 0 {
		m.WinRate = float64(m.Wins) / float64(m.TotalTrades)
	}

	// Avg win/loss
	if m.Wins > 0 || m.Losses > 0 {
		var totalWins, totalLosses float64
		for _, pos := range pf.Positions {
			if pos.ClosedAt != nil {
				if pos.RealizedPnL > 0 {
					totalWins += pos.RealizedPnL
				} else if pos.RealizedPnL < 0 {
					totalLosses += pos.RealizedPnL
				}
			}
		}
		if m.Wins > 0 {
			m.AvgWin = totalWins / float64(m.Wins)
		}
		if m.Losses > 0 {
			m.AvgLoss = totalLosses / float64(m.Losses)
		}
	}

	// Avg holding period
	if len(pf.Positions) > 0 {
		var totalDays float64
		var count int
		for _, pos := range pf.Positions {
			if pos.ClosedAt != nil {
				days := pos.ClosedAt.Sub(pos.OpenedAt).Hours() / 24.0
				totalDays += days
				count++
			}
		}
		if count > 0 {
			m.AvgHoldingDays = totalDays / float64(count)
		}
	}

	// Max drawdown from equity curve
	if len(equityCurve) > 0 {
		peak := equityCurve[0].Equity
		maxDD := 0.0
		for _, pt := range equityCurve {
			if pt.Equity > peak {
				peak = pt.Equity
			}
			dd := pt.Equity - peak
			if dd < maxDD {
				maxDD = dd
			}
		}
		m.MaxDrawdown = maxDD
		m.FinalEquity = equityCurve[len(equityCurve)-1].Equity
	} else {
		m.FinalEquity = pf.Cash
	}

	// Return percentage
	if m.InitialCash > 0 {
		m.ReturnPct = ((m.FinalEquity - m.InitialCash) / m.InitialCash) * 100.0
	}

	return m
}

// WriteMetricsJSON writes metrics to a JSON file
func WriteMetricsJSON(outputDir string, metrics Metrics) error {
	filePath := filepath.Join(outputDir, "metrics.json")
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(metrics)
}
