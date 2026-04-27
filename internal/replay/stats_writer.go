package replay

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteStatsJSON writes comprehensive stats to stats.json.
func WriteStatsJSON(outputDir string, metrics Metrics) error {
	filePath := filepath.Join(outputDir, "stats.json")
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(metrics)
}

// WriteStatsMarkdown writes a human-readable stats summary to stats.md.
func WriteStatsMarkdown(outputDir string, m Metrics, alerts *AlertService) error {
	filePath := filepath.Join(outputDir, "stats.md")
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	var sb strings.Builder

	sb.WriteString("# Backtest Stats Summary\n\n")

	// Portfolio Overview
	sb.WriteString("## Portfolio\n\n")
	sb.WriteString(fmt.Sprintf("| Metric | Value |\n"))
	sb.WriteString(fmt.Sprintf("|---|---|\n"))
	sb.WriteString(fmt.Sprintf("| Initial Cash | ₹%.2f |\n", m.InitialCash))
	sb.WriteString(fmt.Sprintf("| Final Equity | ₹%.2f |\n", m.FinalEquity))
	sb.WriteString(fmt.Sprintf("| Return | %.2f%% |\n", m.ReturnPct))
	sb.WriteString(fmt.Sprintf("| Realized PnL | ₹%.2f |\n", m.RealizedPnL))
	sb.WriteString(fmt.Sprintf("| Unrealized PnL | ₹%.2f |\n", m.UnrealizedPnL))
	sb.WriteString(fmt.Sprintf("| Peak Equity | ₹%.2f |\n", m.PeakEquity))
	sb.WriteString(fmt.Sprintf("| Max Drawdown | ₹%.2f (%.2f%%) |\n", m.MaxDrawdown, m.MaxDrawdownPct))
	sb.WriteString("\n")

	// Trade Statistics
	sb.WriteString("## Trades\n\n")
	sb.WriteString(fmt.Sprintf("| Metric | Value |\n"))
	sb.WriteString(fmt.Sprintf("|---|---|\n"))
	sb.WriteString(fmt.Sprintf("| Total Trades | %d |\n", m.TotalTrades))
	sb.WriteString(fmt.Sprintf("| Closed | %d |\n", m.ClosedTrades))
	sb.WriteString(fmt.Sprintf("| Open | %d |\n", m.OpenTrades))
	sb.WriteString(fmt.Sprintf("| Wins | %d |\n", m.Wins))
	sb.WriteString(fmt.Sprintf("| Losses | %d |\n", m.Losses))
	sb.WriteString(fmt.Sprintf("| Win Rate | %.1f%% |\n", m.WinRate*100))
	sb.WriteString(fmt.Sprintf("| Avg Win | ₹%.2f |\n", m.AvgWin))
	sb.WriteString(fmt.Sprintf("| Avg Loss | ₹%.2f |\n", m.AvgLoss))
	sb.WriteString(fmt.Sprintf("| Expectancy | ₹%.2f |\n", m.Expectancy))

	// Profit factor display
	if math.IsInf(m.ProfitFactor, 1) {
		sb.WriteString(fmt.Sprintf("| Profit Factor | ∞ (no losses) |\n"))
	} else {
		sb.WriteString(fmt.Sprintf("| Profit Factor | %.2f |\n", m.ProfitFactor))
	}

	sb.WriteString(fmt.Sprintf("| Gross Profit | ₹%.2f |\n", m.GrossProfit))
	sb.WriteString(fmt.Sprintf("| Gross Loss | ₹%.2f |\n", m.GrossLoss))
	sb.WriteString(fmt.Sprintf("| Avg Hold Time | %.1f days |\n", m.AvgHoldingDays))
	sb.WriteString("\n")

	// Fees
	sb.WriteString("## Fees\n\n")
	sb.WriteString(fmt.Sprintf("| Metric | Value |\n"))
	sb.WriteString(fmt.Sprintf("|---|---|\n"))
	sb.WriteString(fmt.Sprintf("| Total Fees Paid | ₹%.2f |\n", m.TotalFeesPaid))
	sb.WriteString(fmt.Sprintf("| Avg Fee / Trade | ₹%.2f |\n", m.AvgFeePerTrade))
	sb.WriteString("\n")

	// Daily
	sb.WriteString("## Daily\n\n")
	sb.WriteString(fmt.Sprintf("| Metric | Value |\n"))
	sb.WriteString(fmt.Sprintf("|---|---|\n"))
	sb.WriteString(fmt.Sprintf("| Total Ticks | %d |\n", m.TotalTicks))
	sb.WriteString(fmt.Sprintf("| Ticks In Market | %d |\n", m.TicksInMarket))
	sb.WriteString(fmt.Sprintf("| Best Day PnL | ₹%.2f |\n", m.BestDayPnL))
	sb.WriteString(fmt.Sprintf("| Worst Day PnL | ₹%.2f |\n", m.WorstDayPnL))
	sb.WriteString("\n")

	// Alerts
	if alerts != nil && len(alerts.Alerts) > 0 {
		sb.WriteString("## Alerts Log\n\n")
		sb.WriteString("| Time | Level | Type | Message |\n")
		sb.WriteString("|---|---|---|---|\n")
		for _, a := range alerts.Alerts {
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", a.Time.Format("2006-01-02 15:04"), a.Level, a.Type, a.Message))
		}
		sb.WriteString("\n")
	}

	_, err = f.WriteString(sb.String())
	return err
}

// WriteDailyCSV writes a daily equity/drawdown/pnl CSV.
func WriteDailyCSV(outputDir string, equityCurve []EquityPoint) error {
	filePath := filepath.Join(outputDir, "daily.csv")
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	_ = w.Write([]string{
		"date", "equity", "equity_mid", "cash",
		"unrealized_pnl", "realized_pnl",
		"peak", "drawdown", "drawdown_pct",
		"daily_pnl", "open_positions",
	})

	for i, pt := range equityCurve {
		dailyPnL := 0.0
		if i > 0 {
			dailyPnL = pt.Equity - equityCurve[i-1].Equity
		}

		drawdownPct := 0.0
		if pt.Peak > 0 {
			drawdownPct = (pt.Drawdown / pt.Peak) * 100.0
		}

		_ = w.Write([]string{
			pt.Time.UTC().Format(time.RFC3339),
			fmt.Sprintf("%.2f", pt.Equity),
			fmt.Sprintf("%.2f", pt.MidEquity),
			fmt.Sprintf("%.2f", pt.Cash),
			fmt.Sprintf("%.2f", pt.Unrealized),
			fmt.Sprintf("%.2f", pt.Realized),
			fmt.Sprintf("%.2f", pt.Peak),
			fmt.Sprintf("%.2f", pt.Drawdown),
			fmt.Sprintf("%.2f", drawdownPct),
			fmt.Sprintf("%.2f", dailyPnL),
			fmt.Sprintf("%d", pt.OpenPositions),
		})
	}

	return nil
}
