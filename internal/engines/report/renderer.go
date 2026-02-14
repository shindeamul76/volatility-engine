package report

import (
	"fmt"
	"strings"

	"volatility-engine/internal/domain/risk"
)

// Renderer converts RiskReport into other formats.
type Renderer struct{}

// ToMarkdown generates a markdown summary of the risk report.
func (r *Renderer) ToMarkdown(report risk.StrategyRiskReport) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Risk Report: %s\n\n", report.Candidate.StrategyType))
	sb.WriteString(fmt.Sprintf("**ID**: %s\n", report.Candidate.ID))
	sb.WriteString(fmt.Sprintf("**Generated**: %s\n\n", report.Meta.AsOf.Format("2006-01-02 15:04:05")))

	// 1. Payoff Profile
	sb.WriteString("## Payoff Profile (at Expiry)\n")
	payoff := report.Payoff
	sb.WriteString(fmt.Sprintf("- **Max Profit**: %.2f\n", payoff.MaxProfit))
	sb.WriteString(fmt.Sprintf("- **Max Loss**: %.2f\n", payoff.MaxLoss))
	if len(payoff.Breakevens) > 0 {
		sb.WriteString("- **Breakevens**: ")
		for _, be := range payoff.Breakevens {
			sb.WriteString(fmt.Sprintf("%.2f ", be))
		}
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("- **Defined Risk**: %v\n", !payoff.UnboundedLoss))
	sb.WriteString(fmt.Sprintf("- **Risk/Reward**: %.2f\n\n", payoff.RiskReward))

	// 2. Scenario Analysis
	sb.WriteString("## Scenario Analysis\n")
	sb.WriteString(fmt.Sprintf("Evaluated %d scenarios across Spot, Vol, and Time.\n", len(report.Scenarios.Surface)))

	sb.WriteString(fmt.Sprintf("- **Worst Case P&L (on grid)**: %.2f\n", report.Scenarios.WorstCase.PnL))
	// MaxDrawdown and MaxWin not explicitly tracked in new report format yet.

	sb.WriteString("\n### Current Greeks\n")
	sb.WriteString(fmt.Sprintf("- **Delta**: %.2f\n", report.Greeks.Delta))
	sb.WriteString(fmt.Sprintf("- **Gamma**: %.4f\n", report.Greeks.Gamma))
	sb.WriteString(fmt.Sprintf("- **Vega**: %.2f\n", report.Greeks.Vega))
	sb.WriteString(fmt.Sprintf("- **Theta**: %.2f\n", report.Greeks.Theta))

	return sb.String()
}
