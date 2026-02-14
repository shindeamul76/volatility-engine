package report

import (
	"fmt"
	"strings"

	"volatility-engine/internal/domain/risk"
)

type MarkdownRenderer struct{}

func (r *MarkdownRenderer) Render(report risk.StrategyRiskReport) ([]byte, error) {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Risk Report: %s (%s)\n\n", report.Candidate.ID, report.Candidate.StrategyType))

	meta := report.Meta
	sb.WriteString("## Meta\n")
	sb.WriteString(fmt.Sprintf("- **Underlying**: %s (Spot: %.2f)\n", meta.Underlying, meta.Spot))
	sb.WriteString(fmt.Sprintf("- **Expiry**: %s\n", meta.Expiry))
	sb.WriteString(fmt.Sprintf("- **Regime**: %s (IV30: %.2f%%, Rank: %.2f)\n", meta.Regime, meta.IV30*100, meta.IVRank))
	sb.WriteString("\n")

	// P&L Summary
	sb.WriteString("## P&L Profile\n")
	sb.WriteString("| Metric | Value |\n")
	sb.WriteString("|---|---|\n")
	sb.WriteString(fmt.Sprintf("| Max Profit | %.2f |\n", report.Payoff.MaxProfit))
	sb.WriteString(fmt.Sprintf("| Max Loss | %.2f |\n", report.Payoff.MaxLoss))
	if report.Payoff.UnboundedLoss {
		sb.WriteString("| **Risk Type** | **UNBOUNDED LOSS** |\n")
	} else {
		sb.WriteString("| Risk Type | Defined Risk |\n")
	}
	sb.WriteString(fmt.Sprintf("| Risk/Reward | %.2f |\n", report.Payoff.RiskReward))

	beStr := ""
	for _, b := range report.Payoff.Breakevens {
		beStr += fmt.Sprintf("%.2f, ", b)
	}
	if len(beStr) > 0 {
		beStr = beStr[:len(beStr)-2]
	}
	sb.WriteString(fmt.Sprintf("| Breakevens | %s |\n", beStr))
	sb.WriteString("\n")

	// Greeks
	sb.WriteString("## Net Greeks (Now)\n")
	sb.WriteString(fmt.Sprintf("- **Delta**: %.2f\n", report.Greeks.Delta))
	sb.WriteString(fmt.Sprintf("- **Gamma**: %.4f\n", report.Greeks.Gamma))
	sb.WriteString(fmt.Sprintf("- **Theta**: %.2f\n", report.Greeks.Theta))
	sb.WriteString(fmt.Sprintf("- **Vega**: %.2f\n", report.Greeks.Vega))
	sb.WriteString("\n")

	// Scenarios
	sb.WriteString("## Scenario Analysis\n")
	wc := report.Scenarios.WorstCase
	sb.WriteString(fmt.Sprintf("**Worst Case (on grid)**: P&L %.2f\n", wc.PnL))
	sb.WriteString(fmt.Sprintf("- Scenario: Spot %+.1f%%, Vol %+.2f, T+%dd\n",
		wc.Scenario.SpotChangePct*100, wc.Scenario.VolChange, wc.Scenario.DaysForward))
	sb.WriteString("\n")

	sb.WriteString("### Scenario Slice (T+0)\n")
	sb.WriteString("| Spot % | Vol Chg | P&L | Delta |\n")
	sb.WriteString("|---|---|---|---|\n")

	// Filter for T+0
	count := 0
	for _, res := range report.Scenarios.Surface {
		if res.Scenario.DaysForward == 0 && count < 10 {
			// Just sample some interesting ones? Or all T+0?
			// Let's show all T+0
			sb.WriteString(fmt.Sprintf("| %+.1f%% | %+.2f | %.2f | %.2f |\n",
				res.Scenario.SpotChangePct*100, res.Scenario.VolChange, res.PnL, res.Greeks.Delta))
			count++
		}
	}
	if count == 0 {
		sb.WriteString("(No T+0 scenarios found)\n")
	}

	return []byte(sb.String()), nil
}
